import {ComponentFixture, TestBed} from '@angular/core/testing';
import {MessageService} from 'primeng/api';
import {provideRouter} from '@angular/router';
import {BehaviorSubject, of} from 'rxjs';

import {PluginGeoliteComponent} from './plugin-geolite.component';
import {SettingsService} from '../../../services/settings.service';
import {GlobalSettings} from '../../../models/GlobalSettings';

const timer = () => ({ days: 0, hours: 0, minutes: 0, seconds: 0 });

const defaultSettings: GlobalSettings = {
  protocols: { http: false, https: true, socks4: false, socks5: false },
  checker: {
    dynamic_threads: true,
    threads: 250,
    max_threads: 250,
    save_responses: true,
    retries: 2,
    timeout: 7500,
    checker_timer: timer(),
    judges_threads: 1,
    judges_timeout: 1000,
    judges: [],
    judge_timer: timer(),
    use_https_for_socks: true,
    ip_lookup: '',
    standard_header: [],
    proxy_header: []
  },
  scraper: {
    dynamic_threads: true,
    threads: 1,
    max_threads: 1,
    retries: 1,
    timeout: 1000,
    respect_robots_txt: true,
    scraper_timer: timer(),
    scrape_sites: []
  },
  proxy_limits: {
    enabled: false,
    max_per_user: 0,
    exclude_admins: true
  },
  plugins: {
    geolite: {
      enabled: true,
      api_key: '',
      auto_update: false,
      update_timer: timer(),
      last_updated_at: null
    },
    abuseipdb: {
      enabled: false,
      api_key: '',
      max_age_in_days: 30,
      daily_limit: 0,
      daily_remaining: 0,
      daily_reset_at: null,
      last_checked_at: null,
      last_error: ''
    }
  },
  blacklist_timer: timer(),
  blacklist_sources: [],
  website_blacklist: []
};

class SettingsServiceStub {
  settings$ = new BehaviorSubject<GlobalSettings | undefined>(defaultSettings);

  saveGlobalSettings = jasmine.createSpy('saveGlobalSettings').and.returnValue(of({ message: 'Saved' }));
}

describe('PluginGeoliteComponent', () => {
  let component: PluginGeoliteComponent;
  let fixture: ComponentFixture<PluginGeoliteComponent>;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [PluginGeoliteComponent],
      providers: [
        { provide: SettingsService, useClass: SettingsServiceStub },
        provideRouter([]),
        MessageService,
      ]
    })
    .compileComponents();

    fixture = TestBed.createComponent(PluginGeoliteComponent);
    component = fixture.componentInstance;
    fixture.detectChanges();
  });

  it('should create', () => {
    expect(component).toBeTruthy();
  });
});
