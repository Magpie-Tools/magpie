import { Injectable } from '@angular/core';
import { Observable, BehaviorSubject, of } from 'rxjs';
import { filter, map, switchMap, tap } from 'rxjs/operators';
import { GlobalSettings } from '../models/GlobalSettings';
import { HttpService } from './http.service';
import {UserSettings} from '../models/UserSettings';
import {UserService} from './authorization/user.service';
import {NotificationService} from './notification-service.service';
import {DEFAULT_PROXY_TABLE_COLUMNS, normalizeProxyTableColumns} from '../shared/proxy-table/proxy-table-columns';
import {
  DEFAULT_SCRAPE_SOURCE_LIST_COLUMNS,
  normalizeScrapeSourceListColumns
} from '../scraper/scrape-source-list/scrape-source-list-columns';

@Injectable({
  providedIn: 'root'
})
export class SettingsService {
  private settings: GlobalSettings | undefined;
  private userSettings: UserSettings | undefined;
  private settingsSubject = new BehaviorSubject<GlobalSettings | undefined>(undefined);
  public settings$ = this.settingsSubject.asObservable();
  private userSettingsSubject = new BehaviorSubject<UserSettings | undefined>(undefined);
  public userSettings$ = this.userSettingsSubject.asObservable();

  private globalSettingsLoaded = false;

  constructor(
    private http: HttpService,
    private userService: UserService,
    private notification: NotificationService
  ) {
    this.loadSettings();

    this.userService.role$
      .subscribe(role => {
        if (UserService.isLoggedIn()) {
          this.loadUserSettings();
        } else {
          this.clearUserSettings();
        }

        if (role === 'admin') {
          this.fetchGlobalSettings();
          return;
        }

        this.globalSettingsLoaded = false;
        this.settings = undefined;
        this.settingsSubject.next(this.settings);
      });
  }

  loadSettings(): void {
    this.loadUserSettings();

    if (UserService.isAdmin()) {
      this.fetchGlobalSettings();
    }

  }

  private fetchGlobalSettings(): void {
    if (this.globalSettingsLoaded) {
      return;
    }

    this.http.getGlobalSettings().subscribe({
      next: res => {
        this.globalSettingsLoaded = true;
        this.settings = res;
        this.settingsSubject.next(this.settings);
      },
      error: err => {
        if (err.status === 401 || err.status === 403) {
          this.globalSettingsLoaded = false;
          return;
        }
        this.notification.showError("Error while getting global settings " + err.error.message)
      }
    });
  }

  getGlobalSettings(): GlobalSettings | undefined {
    return this.settings;
  }

  getUserSettings(): UserSettings | undefined {
    return this.userSettings;
  }

  getCheckerSettings(): Observable<GlobalSettings['checker']> {
    return this.settings$.pipe(
      filter((settings): settings is GlobalSettings => settings !== undefined),
      map(settings => settings.checker)
    );
  }

  getScraperSettings(): Observable<GlobalSettings['scraper']> {
    return this.settings$.pipe(
      filter((settings): settings is GlobalSettings => settings !== undefined),
      map(settings => settings.scraper)
    );
  }

  getProxyLimitSettings(): Observable<GlobalSettings['proxy_limits']> {
    return this.settings$.pipe(
      filter((settings): settings is GlobalSettings => settings !== undefined),
      map(settings => settings.proxy_limits)
    );
  }

  getProtocols(): GlobalSettings["protocols"] | undefined {
    return this.settings?.protocols;
  }

  getBlacklistSources(): string[] | undefined {
    return this.settings?.blacklist_sources;
  }

  requeueAllProxies(): Observable<{message: string; proxy_count: number}> {
    return this.http.requeueAllProxies();
  }

  requeueAllScrapeSources(): Observable<{message: string; source_count: number}> {
    return this.http.requeueAllScrapeSources();
  }

  saveUserSettings(formData: any): Observable<any> {
    const payload = this.transformUserSettings(formData);
    this.userSettings = payload;
    this.userSettingsSubject.next(this.userSettings);
    return this.http.saveUserSettings(payload);
  }

  saveProxyListColumns(columns: string[]): Observable<any> {
    const normalizedColumns = normalizeProxyTableColumns(columns);
    const source$ = this.userSettings
      ? of(this.userSettings)
      : this.http.getUserSettings().pipe(
          tap(settings => {
            this.userSettings = settings;
            this.userSettingsSubject.next(settings);
          })
        );

    return source$.pipe(
      map(current => this.buildUserSettingsPayload(current, { proxy_list_columns: normalizedColumns })),
      switchMap(payload => this.http.saveUserSettings(payload).pipe(
        tap(() => {
          this.userSettings = payload;
          this.userSettingsSubject.next(this.userSettings);
        })
      ))
    );
  }

  saveScrapeSourceProxyColumns(columns: string[]): Observable<any> {
    const normalizedColumns = normalizeProxyTableColumns(columns);
    const source$ = this.userSettings
      ? of(this.userSettings)
      : this.http.getUserSettings().pipe(
          tap(settings => {
            this.userSettings = settings;
            this.userSettingsSubject.next(settings);
          })
        );

    return source$.pipe(
      map(current => this.buildUserSettingsPayload(current, { scrape_source_proxy_columns: normalizedColumns })),
      switchMap(payload => this.http.saveUserSettings(payload).pipe(
        tap(() => {
          this.userSettings = payload;
          this.userSettingsSubject.next(this.userSettings);
        })
      ))
    );
  }

  saveScrapeSourceListColumns(columns: string[]): Observable<any> {
    const normalizedColumns = normalizeScrapeSourceListColumns(columns);
    const source$ = this.userSettings
      ? of(this.userSettings)
      : this.http.getUserSettings().pipe(
          tap(settings => {
            this.userSettings = settings;
            this.userSettingsSubject.next(settings);
          })
        );

    return source$.pipe(
      map(current => this.buildUserSettingsPayload(current, { scrape_source_list_columns: normalizedColumns })),
      switchMap(payload => this.http.saveUserSettings(payload).pipe(
        tap(() => {
          this.userSettings = payload;
          this.userSettingsSubject.next(this.userSettings);
        })
      ))
    );
  }

  saveUserScrapingSources(sources: string[]): Observable<any> {
    if (this.userSettings) {
      this.userSettings.scraping_sources = sources
      this.userSettingsSubject.next(this.userSettings);
    }
    return this.http.saveUserScrapingSites(sources)
  }

  private transformUserSettings(formData: any): UserSettings {
    return this.buildUserSettingsPayload(this.userSettings, formData);
  }

  private buildUserSettingsPayload(current: UserSettings | undefined, formData: any): UserSettings {
    const transportProtocol =
      formData.TransportProtocol ??
      formData.transport_protocol ??
      current?.transport_protocol ??
      'tcp';

    return {
      http_protocol: formData.HTTPProtocol ?? formData.http_protocol ?? current?.http_protocol ?? false,
      https_protocol: formData.HTTPSProtocol ?? formData.https_protocol ?? current?.https_protocol ?? true,
      socks4_protocol: formData.SOCKS4Protocol ?? formData.socks4_protocol ?? current?.socks4_protocol ?? false,
      socks5_protocol: formData.SOCKS5Protocol ?? formData.socks5_protocol ?? current?.socks5_protocol ?? false,
      timeout: formData.Timeout ?? formData.timeout ?? current?.timeout ?? 7500,
      retries: formData.Retries ?? formData.retries ?? current?.retries ?? 2,
      UseHttpsForSocks: formData.UseHttpsForSocks ?? formData.use_https_for_socks ?? current?.UseHttpsForSocks ?? true,
      transport_protocol: transportProtocol,
      auto_remove_failing_proxies:
        formData.AutoRemoveFailingProxies ??
        formData.auto_remove_failing_proxies ??
        current?.auto_remove_failing_proxies ??
        false,
      auto_remove_failure_threshold:
        formData.AutoRemoveFailureThreshold ??
        formData.auto_remove_failure_threshold ??
        current?.auto_remove_failure_threshold ??
        3,
      judges: formData.judges ?? current?.judges ?? [],
      scraping_sources: formData.scraping_sources ?? current?.scraping_sources ?? [],
      proxy_list_columns: normalizeProxyTableColumns(
        formData.proxy_list_columns ??
        formData.ProxyListColumns ??
        current?.proxy_list_columns ??
        DEFAULT_PROXY_TABLE_COLUMNS
      ),
      scrape_source_proxy_columns: normalizeProxyTableColumns(
        formData.scrape_source_proxy_columns ??
        formData.ScrapeSourceProxyColumns ??
        current?.scrape_source_proxy_columns ??
        DEFAULT_PROXY_TABLE_COLUMNS
      ),
      scrape_source_list_columns: normalizeScrapeSourceListColumns(
        formData.scrape_source_list_columns ??
        formData.ScrapeSourceListColumns ??
        current?.scrape_source_list_columns ??
        DEFAULT_SCRAPE_SOURCE_LIST_COLUMNS
      ),
    };
  }

  private loadUserSettings(): void {
    if (!UserService.isLoggedIn()) {
      return;
    }

    this.http.getUserSettings().subscribe({
      next: res => {
        this.userSettings = res;
        this.userSettingsSubject.next(this.userSettings);
      },
      error: err => {
        if (err.status === 401 || err.status === 403) {
          this.clearUserSettings();
          return;
        }
        this.notification.showError("Error while getting user settings" + err.error?.message);
      }
    });
  }

  private clearUserSettings(): void {
    this.userSettings = undefined;
    this.userSettingsSubject.next(this.userSettings);
  }

  saveGlobalSettings(formData: any): Observable<any> {
    const payload = this.transformGlobalSettings(formData);
    this.settings = payload;
    this.settingsSubject.next(this.settings);
    return this.http.saveGlobalSettings(payload);
  }

  private transformGlobalSettings(formData: any): GlobalSettings {
    const current = this.settings;

    /* ---------- 1. protocols ---------- */
    const protocols: GlobalSettings['protocols'] = {
      http:   formData?.protocols?.http   ?? current?.protocols?.http   ?? false,
      https:  formData?.protocols?.https  ?? current?.protocols?.https  ?? true,
      socks4: formData?.protocols?.socks4 ?? current?.protocols?.socks4 ?? false,
      socks5: formData?.protocols?.socks5 ?? current?.protocols?.socks5 ?? false
    };

    /* ---------- 2. checker ---------- */
    const checker: GlobalSettings['checker'] = {
      dynamic_threads: formData.dynamic_threads      ?? current?.checker?.dynamic_threads      ?? true,
      threads:         formData.threads              ?? current?.checker?.threads              ?? 250,
      max_threads:     formData.max_threads          ?? current?.checker?.max_threads          ?? current?.checker?.threads ?? 250,
      save_responses:  formData.save_responses       ?? current?.checker?.save_responses       ?? true,
      retries:         formData.retries              ?? current?.checker?.retries              ?? 2,
      timeout:         formData.timeout              ?? current?.checker?.timeout              ?? 7500,

      checker_timer: {
        days:    formData?.checker_timer?.days    ?? current?.checker?.checker_timer?.days    ?? 0,
        hours:   formData?.checker_timer?.hours   ?? current?.checker?.checker_timer?.hours   ?? 6,
        minutes: formData?.checker_timer?.minutes ?? current?.checker?.checker_timer?.minutes ?? 0,
        seconds: formData?.checker_timer?.seconds ?? current?.checker?.checker_timer?.seconds ?? 0
      },

      judges_threads: formData.judges_threads      ?? current?.checker?.judges_threads      ?? 3,
      judges_timeout: formData.judges_timeout      ?? current?.checker?.judges_timeout      ?? 5000,

      judge_timer: {
        days:    formData?.judge_timer?.days    ?? current?.checker?.judge_timer?.days    ?? 0,
        hours:   formData?.judge_timer?.hours   ?? current?.checker?.judge_timer?.hours   ?? 0,
        minutes: formData?.judge_timer?.minutes ?? current?.checker?.judge_timer?.minutes ?? 30,
        seconds: formData?.judge_timer?.seconds ?? current?.checker?.judge_timer?.seconds ?? 0
      },

      judges:             formData.judges             ?? current?.checker?.judges             ?? [],
      use_https_for_socks:formData.use_https_for_socks?? current?.checker?.use_https_for_socks?? true,
      ip_lookup:          formData.iplookup           ?? current?.checker?.ip_lookup          ?? '',

      standard_header: formData.standard_header ?? current?.checker?.standard_header ?? [],

      proxy_header: formData.proxy_header ?? current?.checker?.proxy_header ?? []
    };

    /* ---------- 3. scraper ---------- */
    const scraperSitesFromForm: string[] | undefined = (() => {
      if (Array.isArray(formData.scrape_sites)) {
        return formData.scrape_sites as string[];
      }

      if (typeof formData.scrape_sites === 'string') {
        return formData.scrape_sites
          .split(/\r?\n/)
          .flatMap((segment: string) =>
            segment
              .split(/,(?=\s*https?:\/\/)/)
              .map((site: string) => site.trim())
              .filter((site: string) => site.length > 0)
          );
      }

      return undefined;
    })();

    const normalizedSites: string[] = (scraperSitesFromForm ?? current?.scraper?.scrape_sites ?? [])
      .map((site: string) => site.trim())
      .filter((site: string) => site.length > 0);

    const scrapeSites: string[] = Array.from(new Set<string>(normalizedSites));

    const scraper: GlobalSettings['scraper'] = {
      dynamic_threads: formData.scraper_dynamic_threads ?? current?.scraper?.dynamic_threads ?? true,
      threads:         formData.scraper_threads         ?? current?.scraper?.threads         ?? 250,
      max_threads:     formData.scraper_max_threads     ?? current?.scraper?.max_threads     ?? current?.scraper?.threads ?? 500,
      retries:         formData.scraper_retries         ?? current?.scraper?.retries         ?? 2,
      timeout:         formData.scraper_timeout         ?? current?.scraper?.timeout         ?? 7500,
      respect_robots_txt: formData.scraper_respect_robots_txt ?? current?.scraper?.respect_robots_txt ?? true,

      scraper_timer: {
        days:    formData?.scraper_timer?.days    ?? current?.scraper?.scraper_timer?.days    ?? 0,
        hours:   formData?.scraper_timer?.hours   ?? current?.scraper?.scraper_timer?.hours   ?? 9,
        minutes: formData?.scraper_timer?.minutes ?? current?.scraper?.scraper_timer?.minutes ?? 0,
        seconds: formData?.scraper_timer?.seconds ?? current?.scraper?.scraper_timer?.seconds ?? 0
      },

      scrape_sites: scrapeSites
    };

    /* ---------- 4. proxy limits ---------- */
    const proxy_limits: GlobalSettings['proxy_limits'] = {
      enabled:        formData.proxy_limit_enabled        ?? current?.proxy_limits?.enabled        ?? false,
      max_per_user:   formData.proxy_limit_max_per_user   ?? current?.proxy_limits?.max_per_user   ?? 0,
      exclude_admins: formData.proxy_limit_exclude_admins ?? current?.proxy_limits?.exclude_admins ?? true
    };

    /* ---------- 5. blacklist ---------- */
    const blacklist_timer: GlobalSettings['blacklist_timer'] = {
      days:    formData?.blacklist_timer?.days    ?? current?.blacklist_timer?.days    ?? 0,
      hours:   formData?.blacklist_timer?.hours   ?? current?.blacklist_timer?.hours   ?? 6,
      minutes: formData?.blacklist_timer?.minutes ?? current?.blacklist_timer?.minutes ?? 0,
      seconds: formData?.blacklist_timer?.seconds ?? current?.blacklist_timer?.seconds ?? 0
    };

    const rawBlacklistSources =
      formData.blacklist_sources ??
      formData.blacklisted ??
      current?.blacklist_sources ??
      [];

    const blacklistSourceList = (rawBlacklistSources as string[]) ?? [];

    const blacklist_sources: string[] = blacklistSourceList
      .map(source => source.trim())
      .filter(source => source.length > 0);

    const rawWebsiteBlacklist = formData.website_blacklist ?? current?.website_blacklist ?? [];
    const website_blacklist: string[] = Array.from(
      new Set(
        (rawWebsiteBlacklist as string[])
          .map(entry => entry.trim())
          .filter(entry => entry.length > 0)
      )
    );

    /* ---------- 6. plugins ---------- */
    const geoliteForm = formData.plugins?.geolite ?? formData.geolite ?? {};
    const geoliteTimer = geoliteForm.update_timer ?? {};

    const geolite: GlobalSettings['plugins']['geolite'] = {
      enabled: geoliteForm.enabled ?? current?.plugins?.geolite?.enabled ?? false,
      api_key: geoliteForm.api_key ?? current?.plugins?.geolite?.api_key ?? '',
      auto_update: geoliteForm.auto_update ?? current?.plugins?.geolite?.auto_update ?? false,
      update_timer: {
        days: geoliteTimer?.days ?? current?.plugins?.geolite?.update_timer?.days ?? 1,
        hours: geoliteTimer?.hours ?? current?.plugins?.geolite?.update_timer?.hours ?? 0,
        minutes: geoliteTimer?.minutes ?? current?.plugins?.geolite?.update_timer?.minutes ?? 0,
        seconds: geoliteTimer?.seconds ?? current?.plugins?.geolite?.update_timer?.seconds ?? 0
      },
      last_updated_at: geoliteForm.last_updated_at ?? current?.plugins?.geolite?.last_updated_at ?? null
    };
    const plugins: GlobalSettings['plugins'] = { geolite };

    /* ---------- final shape ---------- */
    return { protocols, checker, scraper, proxy_limits, plugins, blacklist_sources, blacklist_timer, website_blacklist };
  }

}
