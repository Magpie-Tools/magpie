import {ChangeDetectorRef, Component, OnDestroy, OnInit, signal} from '@angular/core';
import {RouterLink} from '@angular/router';
import {FormBuilder, FormGroup, ReactiveFormsModule} from '@angular/forms';
import {Subject, interval} from 'rxjs';
import {filter, startWith, takeUntil} from 'rxjs/operators';

import {ButtonModule} from 'primeng/button';
import {InputTextModule} from 'primeng/inputtext';
import {InputNumberModule} from 'primeng/inputnumber';
import {ToggleSwitchModule} from 'primeng/toggleswitch';

import {GlobalSettings} from '../../../models/GlobalSettings';
import {SettingsService} from '../../../services/settings.service';
import {NotificationService} from '../../../services/notification-service.service';

@Component({
  selector: 'app-plugin-abuseipdb',
  standalone: true,
  imports: [
    RouterLink,
    ReactiveFormsModule,
    ButtonModule,
    InputTextModule,
    InputNumberModule,
    ToggleSwitchModule
  ],
  templateUrl: './plugin-abuseipdb.component.html',
  styleUrl: './plugin-abuseipdb.component.scss'
})
export class PluginAbuseIPDBComponent implements OnInit, OnDestroy {
  form: FormGroup;
  pluginEnabled = signal(false);
  quotaLimit = signal<number | null>(null);
  quotaRemaining = signal<number | null>(null);
  quotaResetLabel = signal('Unknown');
  lastCheckedLabel = signal('Never');
  lastError = signal('');
  private destroy$ = new Subject<void>();

  constructor(
    private fb: FormBuilder,
    private settingsService: SettingsService,
    private notification: NotificationService,
    private cdr: ChangeDetectorRef
  ) {
    this.form = this.fb.group({
      enabled: [false],
      api_key: [''],
      max_age_in_days: [30]
    });
  }

  ngOnInit(): void {
    this.settingsService.settings$
      .pipe(
        filter((settings): settings is GlobalSettings => !!settings),
        takeUntil(this.destroy$)
      )
      .subscribe(settings => this.updateFormWithSettings(settings));

    this.form.get('enabled')?.valueChanges
      .pipe(takeUntil(this.destroy$))
      .subscribe(() => this.syncControlStates());

    interval(60 * 60 * 1000)
      .pipe(startWith(0), takeUntil(this.destroy$))
      .subscribe(() => {
        this.settingsService.refreshGlobalSettings().subscribe({ error: () => {} });
      });

    this.syncControlStates();
  }

  ngOnDestroy(): void {
    this.destroy$.next();
    this.destroy$.complete();
  }

  onSubmit(): void {
    if (this.form.invalid) {
      return;
    }

    const raw = this.form.getRawValue();
    const payload = {
      plugins: {
        abuseipdb: {
          enabled: !!raw.enabled,
          api_key: typeof raw.api_key === 'string' ? raw.api_key.trim() : '',
          max_age_in_days: raw.max_age_in_days ?? 30
        }
      }
    };

    this.settingsService.saveGlobalSettings(payload).subscribe({
      next: (resp) => {
        this.notification.showSuccess(resp.message ?? 'Settings saved');
        this.form.markAsPristine();
        this.cdr.markForCheck();
      },
      error: (err) => {
        console.error('Error saving AbuseIPDB settings:', err);
        this.notification.showError('Failed to save AbuseIPDB settings: ' + (err?.error?.message ?? 'Unknown error'));
      }
    });
  }

  quotaUsagePercent(): number {
    const limit = this.quotaLimit();
    const remaining = this.quotaRemaining();
    if (!limit || remaining === null || remaining === undefined) {
      return 0;
    }
    return Math.max(0, Math.min(100, Math.round(((limit - remaining) / limit) * 100)));
  }

  quotaDisplay(): string {
    const limit = this.quotaLimit();
    const remaining = this.quotaRemaining();
    if (!limit) {
      return 'Unknown';
    }
    return `${remaining ?? 0} / ${limit}`;
  }

  private updateFormWithSettings(settings: GlobalSettings): void {
    const abuseipdb = settings.plugins.abuseipdb;
    this.form.patchValue({
      enabled: abuseipdb?.enabled ?? false,
      api_key: abuseipdb?.api_key ?? '',
      max_age_in_days: abuseipdb?.max_age_in_days ?? 30
    }, { emitEvent: false });

    this.quotaLimit.set(abuseipdb?.daily_limit || null);
    this.quotaRemaining.set(abuseipdb?.daily_limit ? abuseipdb.daily_remaining : null);
    this.quotaResetLabel.set(this.formatTimestamp(abuseipdb?.daily_reset_at));
    this.lastCheckedLabel.set(this.formatTimestamp(abuseipdb?.last_checked_at));
    this.lastError.set(abuseipdb?.last_error ?? '');
    this.form.markAsPristine();
    this.syncControlStates();
  }

  private syncControlStates(): void {
    const enabled = !!this.form.get('enabled')?.value;
    const apiKey = this.form.get('api_key');
    const maxAge = this.form.get('max_age_in_days');
    this.pluginEnabled.set(enabled);

    if (enabled) {
      apiKey?.enable({ emitEvent: false });
      maxAge?.enable({ emitEvent: false });
    } else {
      apiKey?.disable({ emitEvent: false });
      maxAge?.disable({ emitEvent: false });
    }

    this.cdr.markForCheck();
  }

  private formatTimestamp(timestamp?: string | null): string {
    if (!timestamp) {
      return 'Unknown';
    }

    const parsed = new Date(timestamp);
    if (isNaN(parsed.getTime())) {
      return timestamp;
    }

    return parsed.toLocaleString();
  }
}
