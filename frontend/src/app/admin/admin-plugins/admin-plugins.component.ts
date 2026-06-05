import {Component, computed, OnDestroy, OnInit, signal} from '@angular/core';
import {FormsModule} from '@angular/forms';
import {RouterLink} from '@angular/router';
import {SettingsService} from '../../services/settings.service';
import {GlobalSettings} from '../../models/GlobalSettings';
import {Subject} from 'rxjs';
import {filter, takeUntil} from 'rxjs/operators';
import {NotificationService} from '../../services/notification-service.service';
import {ToggleSwitchChangeEvent, ToggleSwitchModule} from 'primeng/toggleswitch';
import {TooltipModule} from 'primeng/tooltip';

@Component({
  selector: 'app-admin-plugins',
  standalone: true,
  imports: [
    RouterLink,
    FormsModule,
    ToggleSwitchModule,
    TooltipModule
  ],
  templateUrl: './admin-plugins.component.html',
  styleUrl: './admin-plugins.component.scss'
})
export class AdminPluginsComponent implements OnInit, OnDestroy {
  plugins = [
    {
      id: 'geolite',
      name: 'GeoLite',
      provider: 'MaxMind',
      logo: 'https://media.licdn.com/dms/image/v2/C560BAQHAOUxYoh2u1Q/company-logo_200_200/company-logo_200_200/0/1671072899861/maxmind_logo?e=2147483647&v=beta&t=WWP-k6AqK1YM0ePQFUi28aEUGjpcuLPSsdKdCSS1940',
      route: '/plugins/geolite'
    },
    {
      id: 'abuseipdb',
      name: 'AbuseIPDB',
      provider: 'AbuseIPDB',
      logo: 'https://www.abuseipdb.com/favicon.ico',
      route: '/plugins/abuseipdb'
    }
  ];

  settings = signal<GlobalSettings | undefined>(undefined);
  pluginStates = computed(() => ({
    geolite: this.settings()?.plugins?.geolite?.enabled ?? false,
    abuseipdb: this.settings()?.plugins?.abuseipdb?.enabled ?? false
  }));
  pendingPluginIds = signal<ReadonlySet<string>>(new Set());
  private destroy$ = new Subject<void>();

  constructor(
    private settingsService: SettingsService,
    private notification: NotificationService
  ) {}

  ngOnInit(): void {
    this.settingsService.settings$
      .pipe(
        filter((settings): settings is GlobalSettings => !!settings),
        takeUntil(this.destroy$)
      )
      .subscribe(settings => {
        this.settings.set(settings);
      });
  }

  ngOnDestroy(): void {
    this.destroy$.next();
    this.destroy$.complete();
  }

  isPluginEnabled(pluginId: string): boolean {
    if (pluginId === 'geolite') {
      return this.pluginStates().geolite;
    }
    if (pluginId === 'abuseipdb') {
      return this.pluginStates().abuseipdb;
    }
    return true;
  }

  isPluginTogglePending(pluginId: string): boolean {
    return this.pendingPluginIds().has(pluginId);
  }

  isPluginIncomplete(pluginId: string): boolean {
    return !!this.getPluginIncompleteReason(pluginId);
  }

  getPluginIncompleteReason(pluginId: string): string | null {
    if (pluginId === 'geolite') {
      const geolite = this.settings()?.plugins?.geolite;
      if (geolite?.enabled && !geolite.api_key?.trim()) {
        return 'GeoLite is enabled but missing a MaxMind license key.';
      }
    }

    if (pluginId === 'abuseipdb') {
      const abuseipdb = this.settings()?.plugins?.abuseipdb;
      if (abuseipdb?.enabled && !abuseipdb.api_key?.trim()) {
        return 'AbuseIPDB is enabled but missing an API key.';
      }
    }

    return null;
  }

  togglePlugin(pluginId: string, event: ToggleSwitchChangeEvent): void {
    if (this.isPluginTogglePending(pluginId) || !this.settings()) {
      return;
    }

    if (pluginId !== 'geolite' && pluginId !== 'abuseipdb') {
      return;
    }

    const previousSettings = this.settings();
    const nextEnabled = event.checked;
    this.setPluginEnabled(pluginId, nextEnabled);
    this.setPluginPending(pluginId, true);

    const pluginsPayload = pluginId === 'geolite'
      ? { geolite: { enabled: nextEnabled } }
      : { abuseipdb: { enabled: nextEnabled } };

    this.settingsService.saveGlobalSettings({ plugins: pluginsPayload }).subscribe({
      next: () => {
        this.setPluginPending(pluginId, false);
      },
      error: (err) => {
        if (previousSettings) {
          this.settings.set(previousSettings);
        }
        this.setPluginPending(pluginId, false);
        this.notification.showError('Failed to update plugin: ' + (err?.error?.message ?? 'Unknown error'));
      }
    });
  }

  private setPluginEnabled(pluginId: string, enabled: boolean): void {
    const current = this.settings();
    if (!current || (pluginId !== 'geolite' && pluginId !== 'abuseipdb')) {
      return;
    }

    const plugins = pluginId === 'geolite'
      ? {
          ...current.plugins,
          geolite: {
            ...current.plugins.geolite,
            enabled
          }
        }
      : {
          ...current.plugins,
          abuseipdb: {
            ...current.plugins.abuseipdb,
            enabled
          }
        };

    this.settings.set({
      ...current,
      plugins
    });
  }

  private setPluginPending(pluginId: string, pending: boolean): void {
    const next = new Set(this.pendingPluginIds());
    if (pending) {
      next.add(pluginId);
    } else {
      next.delete(pluginId);
    }
    this.pendingPluginIds.set(next);
  }
}
