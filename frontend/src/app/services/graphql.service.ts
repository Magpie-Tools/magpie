import {Injectable} from '@angular/core';
import {HttpClient, HttpErrorResponse} from '@angular/common/http';
import {environment} from '../../environments/environment';
import {Observable, throwError} from 'rxjs';
import {map, catchError} from 'rxjs/operators';

export const DASHBOARD_BACKEND_UNAVAILABLE_ERROR = 'dashboard_backend_unavailable';

const DASHBOARD_QUERY = `#graphql
  query DashboardData {
    viewer {
      dashboard {
        totalChecks
        totalScraped
        totalChecksWeek
        totalScrapedWeek
        reputationBreakdown {
          good
          neutral
          poor
          unknown
        }
        countryBreakdown {
          country
          count
        }
        judgeValidProxies {
          judgeUrl
          eliteProxies
          anonymousProxies
          transparentProxies
        }
      }
      proxyCount
      proxyLimit
      recentProxyChecks(limit: 8) {
        id
        ip
        port
        responseTime
        alive
        latestCheck
      }
      fastestAliveProxies(limit: 100) {
        id
        ip
        port
        responseTime
        country
        reputationLabel
        reputationScore
        latestCheck
      }
      proxyHistory(limit: 168) {
        count
        recordedAt
      }
      proxySnapshots {
        alive {
          count
          recordedAt
        }
        scraped {
          count
          recordedAt
        }
      }
      scrapeSourceCount
    }
  }
`;

interface GraphQLError {
  message: string;
}

export interface DashboardQueryData {
  viewer: DashboardViewer;
}

export interface DashboardViewer {
  dashboard: DashboardInfo;
  proxyCount: number;
  proxyLimit: number | null;
  recentProxyChecks: RecentProxyCheck[];
  fastestAliveProxies: FastestAliveProxy[];
  proxyHistory: ProxyHistoryEntry[];
  proxySnapshots: ProxySnapshots;
  scrapeSourceCount: number;
}

export interface DashboardInfo {
  totalChecks: number;
  totalScraped: number;
  totalChecksWeek: number;
  totalScrapedWeek: number;
  reputationBreakdown: ReputationBreakdown;
  countryBreakdown: CountryBreakdownEntry[];
  judgeValidProxies: JudgeValidProxy[];
}

export interface ReputationBreakdown {
  good: number;
  neutral: number;
  poor: number;
  unknown: number;
}

export interface CountryBreakdownEntry {
  country: string;
  count: number;
}

export interface JudgeValidProxy {
  judgeUrl: string;
  eliteProxies: number;
  anonymousProxies: number;
  transparentProxies: number;
}

export interface RecentProxyCheck {
  id: number;
  ip: string;
  port: number;
  responseTime: number;
  alive: boolean;
  latestCheck?: string;
}

export interface FastestAliveProxy {
  id: number;
  ip: string;
  port: number;
  responseTime: number;
  country: string;
  reputationLabel: string;
  reputationScore: number;
  latestCheck?: string;
}

export interface ProxyHistoryEntry {
  count: number;
  recordedAt: string;
}

export interface ProxySnapshotEntry {
  count: number;
  recordedAt: string;
}

export interface ProxySnapshots {
  alive: ProxySnapshotEntry[];
  scraped: ProxySnapshotEntry[];
}

interface GraphQLResponse<T> {
  data?: T;
  errors?: GraphQLError[];
}

@Injectable({ providedIn: 'root' })
export class GraphqlService {
  private graphqlUrl = `${environment.apiUrl}/graphql`;

  constructor(private http: HttpClient) {}

  fetchDashboardData(): Observable<DashboardQueryData> {
    return this.http
      .post<GraphQLResponse<DashboardQueryData>>(this.graphqlUrl, {
        query: DASHBOARD_QUERY
      })
      .pipe(
        map((response) => {
          if (response.errors?.length) {
            const message = response.errors.map((e) => e.message).join('; ');
            throw new Error(message || 'GraphQL query failed');
          }
          if (!response.data) {
            throw new Error('GraphQL query returned no data');
          }
          return response.data;
        }),
        catchError((error) => {
          if (error instanceof HttpErrorResponse && error.status === 0) {
            return throwError(() => new Error(DASHBOARD_BACKEND_UNAVAILABLE_ERROR));
          }

          return throwError(() =>
            error instanceof Error
              ? error
              : new Error(typeof error?.message === 'string' ? error.message : 'Failed to fetch dashboard data')
          );
        })
      );
  }
}
