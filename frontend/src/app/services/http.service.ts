import { Injectable } from '@angular/core';
import {environment} from '../../environments/environment';
import {HttpClient, HttpParams} from '@angular/common/http';
import {User} from '../models/UserModel';
import {jwtToken} from '../models/JwtToken';
import {ProxyPage} from '../models/ProxyInfo';
import {GlobalSettings} from '../models/GlobalSettings';
import {UserSettings} from '../models/UserSettings';
import {ExportSettings} from '../models/ExportSettings';
import {ScrapeSourceExportSettings} from '../models/ScrapeSourceExportSettings';
import {ScrapeSourceInfo} from '../models/ScrapeSourceInfo';
import {ScrapeSourceDetail} from '../models/ScrapeSourceDetail';
import {DashboardInfo} from '../models/DashboardInfo';
import {ChangePassword} from '../models/ChangePassword';
import {DeleteAccount} from '../models/DeleteAccount';
import {ProxyDetail} from '../models/ProxyDetail';
import {ProxyStatistic} from '../models/ProxyStatistic';
import {ProxyStatisticResponseDetail} from '../models/ProxyStatisticResponseDetail';
import {RotatingProxy, CreateRotatingProxy, RotatingProxyNext, RotatingProxyInstance} from '../models/RotatingProxy';
import {map} from 'rxjs/operators';
import {DeleteSettings} from '../models/DeleteSettings';
import {ScrapeSourceDeleteSettings} from '../models/ScrapeSourceDeleteSettings';
import {AddProxiesResponse} from '../models/AddProxiesResponse';
import {ProxyListFilters} from '../models/ProxyListFilters';
import {ScrapeSourceListFilters} from '../models/ScrapeSourceListFilters';
import {ProxyFilterOptions} from '../models/ProxyFilterOptions';

@Injectable({
  providedIn: 'root'
})
export class HttpService {
  private apiUrl = environment.apiUrl;

  constructor(private http: HttpClient) { }

  checkLogin() {
    return this.http.get(this.apiUrl + '/checkLogin')
  }

  registerUser(user: User) {
    return this.http.post<jwtToken>(this.apiUrl + '/register', user)
  }

  loginUser(user: User) {
    return this.http.post<jwtToken>(this.apiUrl + '/login', user)
  }

  requestPasswordReset(payload: { email: string }) {
    return this.http.post<{message: string}>(this.apiUrl + '/forgotPassword', payload);
  }

  resetPasswordWithToken(payload: { token: string; newPassword: string }) {
    return this.http.post<{message: string}>(this.apiUrl + '/resetPassword', payload);
  }

  changePassword(changePassword: ChangePassword) {
    return this.http.post<string>(this.apiUrl + '/changePassword', changePassword)
  }

  deleteAccount(payload: DeleteAccount) {
    return this.http.post<string>(this.apiUrl + '/deleteAccount', payload)
  }

  uploadProxies(formData: FormData) {
    return this.http.post<AddProxiesResponse>(this.apiUrl + '/addProxies', formData);
  }

  deleteProxies(settings: DeleteSettings) {
    return this.http.request<string>('delete', this.apiUrl + '/proxies', {
      body: settings,
    });
  }


  getProxyPage(
    pageNumber: number,
    options?: {
      rows?: number;
      search?: string;
      filters?: ProxyListFilters;
      includeHealth?: boolean;
      includeReputation?: boolean;
      sortField?: string | null;
      sortOrder?: number | null;
    }
  ) {
    let params = new HttpParams();

    if (options?.rows && options.rows > 0) {
      params = params.set('pageSize', options.rows.toString());
    }

    if (options?.search && options.search.trim().length > 0) {
      params = params.set('search', options.search.trim());
    }

    if (options?.includeHealth !== undefined) {
      params = params.set('includeHealth', options.includeHealth ? 'true' : 'false');
    }

    if (options?.includeReputation !== undefined) {
      params = params.set('includeReputation', options.includeReputation ? 'true' : 'false');
    }

    if (options?.sortField && options.sortOrder && options.sortOrder !== 0) {
      params = params.set('sortField', options.sortField);
      params = params.set('sortOrder', options.sortOrder === 1 ? 'asc' : 'desc');
    }

    params = this.appendProxyFilterParams(params, options?.filters);

    return this.http.get<ProxyPage>(`${this.apiUrl}/getProxyPage/${pageNumber}`, { params });
  }

  getProxyFilterOptions() {
    return this.http.get<ProxyFilterOptions>(`${this.apiUrl}/proxyFilters`);
  }

  getProxyCount() {
    return this.http.get<number>(this.apiUrl + '/getProxyCount');
  }

  getProxyDetail(proxyId: number) {
    return this.http.get<ProxyDetail>(`${this.apiUrl}/proxies/${proxyId}`);
  }

  getProxyStatistics(proxyId: number, options?: { limit?: number }) {
    let params = new HttpParams();
    if (options?.limit && options.limit > 0) {
      params = params.set('limit', options.limit.toString());
    }

    return this.http.get<{statistics: ProxyStatistic[]}>(`${this.apiUrl}/proxies/${proxyId}/statistics`, { params })
      .pipe(map(res => res?.statistics ?? []));
  }

  getProxyStatisticResponseBody(proxyId: number, statisticId: number) {
    return this.http
      .get<ProxyStatisticResponseDetail>(`${this.apiUrl}/proxies/${proxyId}/statistics/${statisticId}`)
      .pipe(
        map(res => {
          const regex = res?.regex?.trim();
          return {
            response_body: res?.response_body ?? '',
            regex: regex ? regex : null,
          } as ProxyStatisticResponseDetail;
        })
      );
  }

  getRotatingProxies() {
    return this.http
      .get<{rotating_proxies: RotatingProxy[]}>(`${this.apiUrl}/rotatingProxies`)
      .pipe(map(res => res?.rotating_proxies ?? []));
  }

  getRotatingProxyInstances() {
    return this.http
      .get<{instances: RotatingProxyInstance[]}>(`${this.apiUrl}/rotatingProxies/instances`)
      .pipe(map(res => res?.instances ?? []));
  }

  createRotatingProxy(payload: CreateRotatingProxy) {
    return this.http.post<RotatingProxy>(`${this.apiUrl}/rotatingProxies`, payload);
  }

  deleteRotatingProxy(id: number) {
    return this.http.delete<void>(`${this.apiUrl}/rotatingProxies/${id}`);
  }

  getNextRotatingProxy(id: number) {
    return this.http.post<RotatingProxyNext>(`${this.apiUrl}/rotatingProxies/${id}/next`, {});
  }


  saveGlobalSettings(payload: GlobalSettings) {
    return this.http.post(this.apiUrl + "/saveSettings", payload)
  }

  requeueAllProxies() {
    return this.http.post<{message: string; proxy_count: number}>(`${this.apiUrl}/global/proxies/requeue`, {});
  }

  requeueProxy(proxyId: number) {
    return this.http.post<{message: string; proxy_id: number}>(`${this.apiUrl}/global/proxies/${proxyId}/requeue`, {});
  }

  requeueAllScrapeSources() {
    return this.http.post<{message: string; source_count: number}>(`${this.apiUrl}/global/scrapeSources/requeue`, {});
  }

  requeueScrapeSource(sourceId: number) {
    return this.http.post<{message: string; source_id: number}>(`${this.apiUrl}/global/scrapeSources/${sourceId}/requeue`, {});
  }

  getGlobalSettings() {
    return this.http.get<GlobalSettings>(this.apiUrl + '/global/settings');
  }

  getUserSettings() {
    return this.http.get<UserSettings>(this.apiUrl + '/user/settings');
  }

  saveUserSettings(payload: UserSettings) {
    return this.http.post(this.apiUrl + "/user/settings", payload)
  }

  saveUserScrapingSites(payload: string[]) {
    return this.http.post(this.apiUrl + "/user/scrapingSites", payload)
  }

  getUserRole() {
    return this.http.get<string>(this.apiUrl + '/user/role');
  }

  exportProxies(settings: ExportSettings) {
    return this.http.post(this.apiUrl + '/user/export', settings, {
      responseType: 'text',
    });
  }

  exportScrapeSources(settings: ScrapeSourceExportSettings) {
    return this.http.post(this.apiUrl + '/scrapingSources/export', settings, {
      responseType: 'text',
    });
  }

  uploadScrapeSources(formData: FormData) {
    return this.http.post<{sourceCount: number}>(this.apiUrl + '/scrapingSources', formData);
  }

  getScrapingSourcesCount(options?: { search?: string; filters?: ScrapeSourceListFilters }) {
    let params = new HttpParams();
    if (options?.search && options.search.trim().length > 0) {
      params = params.set('search', options.search.trim());
    }
    params = this.appendScrapeSourceFilterParams(params, options?.filters);

    return this.http.get<number>(this.apiUrl + '/getScrapingSourcesCount', { params });
  }

  getScrapingSourcePage(pageNumber: number, options?: { rows?: number; search?: string; filters?: ScrapeSourceListFilters }) {
    let params = new HttpParams();
    if (options?.rows && options.rows > 0) {
      params = params.set('pageSize', options.rows.toString());
    }
    if (options?.search && options.search.trim().length > 0) {
      params = params.set('search', options.search.trim());
    }
    params = this.appendScrapeSourceFilterParams(params, options?.filters);

    return this.http.get<ScrapeSourceInfo[]>(this.apiUrl + '/getScrapingSourcesPage/' + pageNumber, { params });
  }

  getScrapeSourceDetail(sourceId: number) {
    return this.http.get<ScrapeSourceDetail>(`${this.apiUrl}/scrapingSources/${sourceId}`);
  }

  getScrapeSourceProxyPage(sourceId: number, options?: {
    page?: number;
    rows?: number;
    search?: string;
    filters?: ProxyListFilters;
    includeHealth?: boolean;
    includeReputation?: boolean;
    sortField?: string | null;
    sortOrder?: number | null;
  }) {
    let params = new HttpParams();

    const page = options?.page && options.page > 0 ? options.page : 1;
    params = params.set('page', page.toString());

    if (options?.rows && options.rows > 0) {
      params = params.set('pageSize', options.rows.toString());
    }

    if (options?.search && options.search.trim().length > 0) {
      params = params.set('search', options.search.trim());
    }

    if (options?.includeHealth !== undefined) {
      params = params.set('includeHealth', options.includeHealth ? 'true' : 'false');
    }

    if (options?.includeReputation !== undefined) {
      params = params.set('includeReputation', options.includeReputation ? 'true' : 'false');
    }

    if (options?.sortField && options.sortOrder && options.sortOrder !== 0) {
      params = params.set('sortField', options.sortField);
      params = params.set('sortOrder', options.sortOrder === 1 ? 'asc' : 'desc');
    }

    params = this.appendProxyFilterParams(params, options?.filters);

    return this.http.get<ProxyPage>(`${this.apiUrl}/scrapingSources/${sourceId}/proxies`, { params });
  }

  deleteScrapingSource(proxies: number[] | ScrapeSourceDeleteSettings) {
    return this.http.request<string>('delete', this.apiUrl + '/scrapingSources', {
      body: proxies,
    });
  }

  checkScrapeSource(url: string) {
    const params = new HttpParams().set('url', url);
    return this.http.get<{allowed: boolean; robots_found: boolean; error?: string}>(this.apiUrl + '/scrapingSources/check', { params });
  }

  getRespectRobotsSetting() {
    return this.http.get<{respect_robots_txt: boolean}>(this.apiUrl + '/scrapingSources/respectRobots');
  }

  getDashboardInfo() {
    return this.http.get<DashboardInfo>(this.apiUrl + '/getDashboardInfo');
  }

  private appendProxyFilterParams(params: HttpParams, filters?: ProxyListFilters): HttpParams {
    if (!filters) {
      return params;
    }

    if (filters.status) {
      params = params.set('status', filters.status);
    }

    if (filters.protocols?.length) {
      filters.protocols.forEach(protocol => {
        params = params.append('protocol', protocol);
      });
    }

    if (filters.minHealthOverall && filters.minHealthOverall > 0) {
      params = params.set('minHealthOverall', filters.minHealthOverall.toString());
    }

    if (filters.minHealthHttp && filters.minHealthHttp > 0) {
      params = params.set('minHealthHttp', filters.minHealthHttp.toString());
    }

    if (filters.minHealthHttps && filters.minHealthHttps > 0) {
      params = params.set('minHealthHttps', filters.minHealthHttps.toString());
    }

    if (filters.minHealthSocks4 && filters.minHealthSocks4 > 0) {
      params = params.set('minHealthSocks4', filters.minHealthSocks4.toString());
    }

    if (filters.minHealthSocks5 && filters.minHealthSocks5 > 0) {
      params = params.set('minHealthSocks5', filters.minHealthSocks5.toString());
    }

    if (filters.countries?.length) {
      filters.countries.forEach(country => {
        params = params.append('country', country);
      });
    }

    if (filters.types?.length) {
      filters.types.forEach(type => {
        params = params.append('type', type);
      });
    }

    if (filters.anonymityLevels?.length) {
      filters.anonymityLevels.forEach(level => {
        params = params.append('anonymity', level);
      });
    }

    if (filters.reputationLabels?.length) {
      filters.reputationLabels.forEach(label => {
        params = params.append('reputation', label);
      });
    }

    if (filters.maxTimeout && filters.maxTimeout > 0) {
      params = params.set('maxTimeout', filters.maxTimeout.toString());
    }

    if (filters.maxRetries && filters.maxRetries > 0) {
      params = params.set('maxRetries', filters.maxRetries.toString());
    }

    return params;
  }

  private appendScrapeSourceFilterParams(params: HttpParams, filters?: ScrapeSourceListFilters): HttpParams {
    if (!filters) {
      return params;
    }

    if (filters.protocols?.length) {
      filters.protocols.forEach(protocol => {
        params = params.append('protocol', protocol);
      });
    }

    if (filters.proxyCount && filters.proxyCount > 0) {
      params = params.set('proxyCount', filters.proxyCount.toString());
      params = params.set('proxyCountOperator', filters.proxyCountOperator === '<' ? '<' : '>');
    }

    if (filters.aliveCount && filters.aliveCount > 0) {
      params = params.set('aliveCount', filters.aliveCount.toString());
      params = params.set('aliveCountOperator', filters.aliveCountOperator === '<' ? '<' : '>');
    }

    return params;
  }
}
