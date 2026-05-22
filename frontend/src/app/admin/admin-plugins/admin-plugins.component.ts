import {Component, computed, OnDestroy, OnInit, signal} from '@angular/core';
import {FormsModule} from '@angular/forms';
import {RouterLink} from '@angular/router';
import {SettingsService} from '../../services/settings.service';
import {GlobalSettings} from '../../models/GlobalSettings';
import {Subject} from 'rxjs';
import {filter, takeUntil} from 'rxjs/operators';
import {NotificationService} from '../../services/notification-service.service';
import {ToggleSwitchChangeEvent, ToggleSwitchModule} from 'primeng/toggleswitch';

@Component({
  selector: 'app-admin-plugins',
  standalone: true,
  imports: [
    RouterLink,
    FormsModule,
    ToggleSwitchModule
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
    }
  ];

  settings = signal<GlobalSettings | undefined>(undefined);
  pluginStates = computed(() => ({
    geolite: this.settings()?.plugins?.geolite?.enabled ?? false
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
    return true;
  }

  isPluginTogglePending(pluginId: string): boolean {
    return this.pendingPluginIds().has(pluginId);
  }

  togglePlugin(pluginId: string, event: ToggleSwitchChangeEvent): void {
    if (this.isPluginTogglePending(pluginId) || !this.settings()) {
      return;
    }

    if (pluginId !== 'geolite') {
      return;
    }

    const previousSettings = this.settings();
    const nextEnabled = event.checked;
    this.setPluginEnabled(pluginId, nextEnabled);
    this.setPluginPending(pluginId, true);

    this.settingsService.saveGlobalSettings({ plugins: { geolite: { enabled: nextEnabled } } }).subscribe({
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
    if (!current || pluginId !== 'geolite') {
      return;
    }

    this.settings.set({
      ...current,
      plugins: {
        ...current.plugins,
        geolite: {
          ...current.plugins.geolite,
          enabled
        }
      }
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
