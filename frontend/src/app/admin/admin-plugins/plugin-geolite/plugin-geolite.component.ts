import {ChangeDetectorRef, Component, OnDestroy, OnInit, signal} from '@angular/core';
import {RouterLink} from '@angular/router';
import {FormBuilder, FormGroup, ReactiveFormsModule} from '@angular/forms';
import {Subject} from 'rxjs';
import {filter, takeUntil} from 'rxjs/operators';

import {ButtonModule} from 'primeng/button';
import {CheckboxModule} from 'primeng/checkbox';
import {InputTextModule} from 'primeng/inputtext';
import {SelectModule} from 'primeng/select';
import {Message} from 'primeng/message';
import {ToggleSwitchModule} from 'primeng/toggleswitch';

import {GlobalSettings} from '../../../models/GlobalSettings';
import {SettingsService} from '../../../services/settings.service';
import {NotificationService} from '../../../services/notification-service.service';

@Component({
  selector: 'app-plugin-geolite',
  standalone: true,
  imports: [
    RouterLink,
    ReactiveFormsModule,
    ButtonModule,
    CheckboxModule,
    InputTextModule,
    SelectModule,
    Message,
    ToggleSwitchModule
  ],
  templateUrl: './plugin-geolite.component.html',
  styleUrl: './plugin-geolite.component.scss'
})
export class PluginGeoliteComponent implements OnInit, OnDestroy {
  daysList = Array.from({ length: 31 }, (_, i) => ({ label: `${i} Days`, value: i }));
  hoursList = Array.from({ length: 24 }, (_, i) => ({ label: `${i} Hours`, value: i }));
  minutesList = Array.from({ length: 60 }, (_, i) => ({ label: `${i} Minutes`, value: i }));
  secondsList = Array.from({ length: 60 }, (_, i) => ({ label: `${i} Seconds`, value: i }));
  form: FormGroup;
  lastUpdatedLabel = 'Never';
  pluginEnabled = signal(false);
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
      auto_update: [false],
      update_timer: this.fb.group({
        days: [1],
        hours: [0],
        minutes: [0],
        seconds: [0]
      }),
      last_updated_at: [null]
    });
  }

  ngOnInit(): void {
    this.settingsService.settings$
      .pipe(
        filter((settings): settings is GlobalSettings => !!settings),
        takeUntil(this.destroy$)
      )
      .subscribe(settings => this.updateFormWithSettings(settings));

    this.form.get('auto_update')?.valueChanges
      .pipe(takeUntil(this.destroy$))
      .subscribe(() => this.syncControlStates());

    this.form.get('enabled')?.valueChanges
      .pipe(takeUntil(this.destroy$))
      .subscribe(() => this.syncControlStates());

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
    const timer = raw.update_timer ?? {};
    const payload = {
      plugins: {
        geolite: {
          enabled: !!raw.enabled,
          api_key: typeof raw.api_key === 'string' ? raw.api_key.trim() : '',
          auto_update: !!raw.auto_update,
          update_timer: {
            days: timer.days ?? 1,
            hours: timer.hours ?? 0,
            minutes: timer.minutes ?? 0,
            seconds: timer.seconds ?? 0
          },
          last_updated_at: raw.last_updated_at ?? null
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
        console.error('Error saving GeoLite settings:', err);
        this.notification.showError('Failed to save GeoLite settings: ' + (err?.error?.message ?? 'Unknown error'));
      }
    });
  }

  get updateTimerGroup(): FormGroup | null {
    return this.form.get('update_timer') as FormGroup | null;
  }

  private updateFormWithSettings(settings: GlobalSettings): void {
    const geolite = settings.plugins.geolite;
    this.form.patchValue({
      enabled: geolite?.enabled ?? false,
      api_key: geolite?.api_key ?? '',
      auto_update: geolite?.auto_update ?? false,
      update_timer: {
        days: geolite?.update_timer?.days ?? 1,
        hours: geolite?.update_timer?.hours ?? 0,
        minutes: geolite?.update_timer?.minutes ?? 0,
        seconds: geolite?.update_timer?.seconds ?? 0
      },
      last_updated_at: geolite?.last_updated_at ?? null
    }, { emitEvent: false });

    this.form.markAsPristine();
    this.lastUpdatedLabel = this.formatTimestamp(geolite?.last_updated_at);
    this.syncControlStates();
  }

  private syncControlStates(): void {
    const enabled = !!this.form.get('enabled')?.value;
    const apiKey = this.form.get('api_key');
    const autoUpdate = this.form.get('auto_update');
    this.pluginEnabled.set(enabled);

    if (enabled) {
      apiKey?.enable({ emitEvent: false });
      autoUpdate?.enable({ emitEvent: false });
    } else {
      apiKey?.disable({ emitEvent: false });
      autoUpdate?.disable({ emitEvent: false });
    }

    const group = this.updateTimerGroup;
    if (!group) {
      return;
    }

    if (enabled && autoUpdate?.value) {
      group.enable({ emitEvent: false });
    } else {
      group.disable({ emitEvent: false });
    }

    this.cdr.markForCheck();
  }

  private formatTimestamp(timestamp?: string | null): string {
    if (!timestamp) {
      return 'Never';
    }

    const parsed = new Date(timestamp);
    if (isNaN(parsed.getTime())) {
      return timestamp;
    }

    return parsed.toLocaleString();
  }
}
