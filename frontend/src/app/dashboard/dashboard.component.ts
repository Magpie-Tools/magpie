import {Component, Inject, LOCALE_ID, OnDestroy, OnInit, signal} from '@angular/core';
import {DecimalPipe} from '@angular/common';
import {Subject} from 'rxjs';
import {finalize, takeUntil} from 'rxjs/operators';
import {ProxyCheck} from '../models/ProxyCheck';
import {KpiCardComponent} from './cards/kpi-card/kpi-card.component';
import {ProxiesPerHourCardComponent} from './cards/proxies-per-hour-card/proxies-per-hour-card.component';
import {ProxyHistoryCardComponent} from './cards/proxy-history-card/proxy-history-card.component';
import {ProxiesPerCountryCardComponent} from './cards/proxies-per-country-card/proxies-per-country-card.component';
import {JudgeByPercentageCardComponent} from './cards/judge-by-percentage-card/judge-by-percentage-card.component';
import {
  CountryBreakdownEntry,
  DASHBOARD_BACKEND_UNAVAILABLE_ERROR,
  DashboardInfo,
  DashboardViewer,
  GraphqlService,
  JudgeValidProxy,
  ProxyHistoryEntry,
  RecentProxyCheck,
  ProxySnapshotEntry,
  ProxySnapshots,
  ReputationBreakdown,
} from '../services/graphql.service';
import {ProxyReputationCardComponent} from './cards/proxy-reputation-card/proxy-reputation-card.component';
import {SkeletonModule} from 'primeng/skeleton';

interface SparklineMetric {
  value: number;
  history: number[];
  displayValue?: string | null;
  change?: number | null;
}

interface DashboardStatus {
  loading: boolean;
  loaded: boolean;
  error?: string;
  backendUnavailable?: boolean;
}

interface ProxyLineI18n {
  title: string;
  proxiesLabel: string;
  limitLabel: string;
  noDataLabel: string;
  tooltipProxiesLabel: string;
  tooltipGainedLabel: string;
  tooltipLostLabel: string;
  tooltipLimitLabel: string;
}

@Component({
  selector: 'app-dashboard',
  templateUrl: './dashboard.component.html',
  imports: [
    DecimalPipe,
    KpiCardComponent,
    ProxiesPerHourCardComponent,
    ProxyHistoryCardComponent,
    ProxiesPerCountryCardComponent,
    JudgeByPercentageCardComponent,
    ProxyReputationCardComponent,
    SkeletonModule
  ],
  styleUrls: ['./dashboard.component.scss']
})
export class DashboardComponent implements OnInit, OnDestroy {
  dashboardInfo = signal<DashboardStatus>({ loading: false, loaded: false });

  conversionRate = signal<SparklineMetric>({ value: 0, history: [] });
  avgOrderValue = signal<SparklineMetric>({ value: 0, history: [] });
  orderQuantity = signal<SparklineMetric>({ value: 0, history: [] });

  proxiesLineData = signal<any>({});
  proxiesLineOptions = signal<any>({});
  private proxiesLineDiff = { gained: [] as number[], lost: [] as number[] };

  majorCountries = signal<Array<{ name: string; value: number; color?: string; percentage: string }>>([]);

  anonymitySummary = signal<{ total: number; change: number } | undefined>(undefined);
  anonymitySegments = signal<Array<{
    name: string;
    count: number;
    change: number;
    share: number;
    barClass: string;
    dotColor: string;
  }>>([]);

  proxyHistory = signal<ProxyCheck[]>([]);

  reputationBreakdown = signal<ReputationBreakdown>({ good: 0, neutral: 0, poor: 0, unknown: 0 });
  reputationChartData = signal<any>({});
  reputationChartOptions = signal<any>({});

  readonly proxiesPerHourTitle: string;
  private readonly localeId: string;
  private readonly numberFormatter: Intl.NumberFormat;
  private readonly proxyLineI18n: ProxyLineI18n;

  judgeTrafficData = signal<Record<string, number>>({});
  judgePeriodOptions = ['Yearly', 'Monthly', 'Weekly'];

  private readonly destroy$ = new Subject<void>();
  proxyHistoryRefreshing = signal(false);
  readonly kpiSkeletons = Array.from({ length: 3 });
  readonly historySkeletons = Array.from({ length: 6 });
  readonly countrySkeletons = Array.from({ length: 5 });
  readonly reputationSkeletons = Array.from({ length: 4 });

  constructor(
    private graphqlService: GraphqlService,
    @Inject(LOCALE_ID) localeId: string
  ) {
    this.localeId = localeId || 'en-US';
    this.numberFormatter = new Intl.NumberFormat(this.localeId);
    this.proxyLineI18n = this.resolveProxyLineI18n(this.localeId);
    this.proxiesPerHourTitle = this.proxyLineI18n.title;
  }

  ngOnInit(): void {
    this.loadDashboard();
  }

  ngOnDestroy(): void {
    this.destroy$.next();
    this.destroy$.complete();
  }

  onProxyHistoryRefresh(): void {
    if (this.proxyHistoryRefreshing()) {
      return;
    }

    this.proxyHistoryRefreshing.set(true);

    this.graphqlService
      .fetchDashboardData()
      .pipe(
        takeUntil(this.destroy$),
        finalize(() => {
          this.proxyHistoryRefreshing.set(false);
        })
      )
      .subscribe({
        next: ({viewer}) => {
          this.applyDashboardData(viewer);
          this.dashboardInfo.update((info) => ({ ...info, error: undefined, backendUnavailable: false }));
        },
        error: (error: Error) => {
          const resolved = this.resolveDashboardError(error, 'Failed to refresh proxy history');
          this.dashboardInfo.update((info) => ({
            ...info,
            error: resolved.message,
            backendUnavailable: resolved.backendUnavailable
          }));
        }
      });
  }

  retryDashboardLoad(): void {
    this.loadDashboard();
  }

  private loadDashboard(): void {
    this.dashboardInfo.set({ loading: true, loaded: false, backendUnavailable: false });
    this.graphqlService
      .fetchDashboardData()
      .pipe(takeUntil(this.destroy$))
      .subscribe({
        next: ({ viewer }) => {
          this.applyDashboardData(viewer);
          this.dashboardInfo.set({ loading: false, loaded: true, backendUnavailable: false });
        },
        error: (error: Error) => {
          const resolved = this.resolveDashboardError(error, 'Failed to load dashboard data');
          this.dashboardInfo.set({
            loading: false,
            loaded: false,
            error: resolved.message,
            backendUnavailable: resolved.backendUnavailable
          });
        }
      });
  }

  private resolveDashboardError(error: unknown, fallbackMessage: string): { message: string; backendUnavailable: boolean } {
    if (error instanceof Error && error.message === DASHBOARD_BACKEND_UNAVAILABLE_ERROR) {
      return {
        message: 'Dashboard is temporarily unavailable because the backend service cannot be reached.',
        backendUnavailable: true
      };
    }

    const message = error instanceof Error && error.message ? error.message : fallbackMessage;
    return { message, backendUnavailable: false };
  }

  private applyDashboardData(viewer: DashboardViewer | undefined): void {
    if (!viewer) {
      return;
    }

    this.updateKpis(viewer.dashboard, viewer.proxyCount, viewer.proxySnapshots, viewer.proxyHistory);
    this.updateCountryBreakdown(viewer.dashboard?.countryBreakdown ?? []);
    this.updateReputationOverview(viewer.dashboard);
    this.updateProxyHistory(viewer.recentProxyChecks ?? []);
    this.updateAnonymitySummary(viewer.dashboard?.judgeValidProxies ?? []);
    this.updateJudgeBreakdown(viewer.dashboard?.judgeValidProxies ?? []);
    this.buildProxiesLineChart(viewer.proxyHistory ?? [], viewer.proxyLimit);
  }

  private updateKpis(
    dashboard: DashboardInfo | undefined,
    proxyCount: number,
    snapshots: ProxySnapshots | undefined,
    proxyHistory: ProxyHistoryEntry[] | undefined
  ): void {
    const aliveSeries = this.extractSnapshotCounts(snapshots?.alive);
    const fallbackAlive = this.resolveAliveFallback(dashboard);
    const aliveValue = aliveSeries.length ? aliveSeries[aliveSeries.length - 1] : fallbackAlive.value;
    const aliveHistory = aliveSeries.length ? aliveSeries.slice(0, -1) : fallbackAlive.history;

    this.conversionRate.set({
      value: aliveValue,
      history: aliveHistory,
      displayValue: aliveValue.toLocaleString()
    });

    const totalSeries = (proxyHistory ?? []).map((entry) => entry.count);
    this.avgOrderValue.set({
      value: proxyCount,
      history: totalSeries.length ? totalSeries : [proxyCount],
      displayValue: proxyCount.toLocaleString()
    });

    const scrapedSeries = this.extractSnapshotCounts(snapshots?.scraped);
    const fallbackScraped = this.resolveScrapedFallback(dashboard);
    const scrapedValue = scrapedSeries.length ? scrapedSeries[scrapedSeries.length - 1] : fallbackScraped.value;
    const scrapedHistory = scrapedSeries.length ? scrapedSeries.slice(0, -1) : fallbackScraped.history;

    this.orderQuantity.set({
      value: scrapedValue,
      history: scrapedHistory,
      displayValue: scrapedValue.toLocaleString()
    });
  }

  private extractSnapshotCounts(entries: ProxySnapshotEntry[] | undefined): number[] {
    if (!Array.isArray(entries) || !entries.length) {
      return [];
    }

    return entries
      .map((entry) => Number(entry?.count ?? 0))
      .filter((value) => Number.isFinite(value) && value >= 0);
  }

  private resolveAliveFallback(dashboard: DashboardInfo | undefined): { value: number; history: number[] } {
    const judgeEntries = dashboard?.judgeValidProxies ?? [];
    if (!judgeEntries.length) {
      return { value: 0, history: [] };
    }

    const totals = judgeEntries.map(
      (entry) => entry.eliteProxies + entry.anonymousProxies + entry.transparentProxies
    );
    const aggregate = totals.reduce((sum, value) => sum + value, 0);

    return {
      value: aggregate,
      history: totals.filter((value) => value > 0)
    };
  }

  private resolveScrapedFallback(dashboard: DashboardInfo | undefined): { value: number; history: number[] } {
    if (!dashboard) {
      return { value: 0, history: [] };
    }

    const totalScraped = dashboard.totalScraped ?? 0;
    const totalScrapedWeek = dashboard.totalScrapedWeek ?? 0;
    const history = totalScrapedWeek > 0 ? [totalScrapedWeek] : [];

    return {
      value: totalScraped,
      history
    };
  }

  private updateCountryBreakdown(breakdown: CountryBreakdownEntry[] = []): void {
    const aggregated = new Map<string, number>();

    breakdown
      .filter((entry) => entry.count > 0)
      .forEach((entry) => {
        const name = this.normalizeCountryBreakdownName(entry.country);
        aggregated.set(name, (aggregated.get(name) ?? 0) + entry.count);
      });

    const sorted = Array.from(aggregated, ([name, value]) => ({name, value}))
      .filter((entry) => entry.value > 0)
      .sort((a, b) => b.value - a.value);
    const total = sorted.reduce((sum, entry) => sum + entry.value, 0);

    this.majorCountries.set(sorted.map((entry) => ({
      name: entry.name,
      value: entry.value,
      percentage: total > 0 ? ((entry.value / total) * 100).toFixed(1) : '0.0'
    })));
  }

  private normalizeCountryBreakdownName(country: string | undefined | null): string {
    const trimmed = country?.trim() ?? '';
    if (!trimmed) {
      return 'Unknown';
    }

    switch (trimmed.toLowerCase()) {
      case 'n/a':
      case 'unknown':
      case 'unk':
        return 'Unknown';
      default:
        return trimmed;
    }
  }

  private updateProxyHistory(proxies: RecentProxyCheck[]): void {
    this.proxyHistory.set(
      proxies
        .map((proxy): ProxyCheck | null => {
          const latest = this.parseDate(proxy.latestCheck);
          if (!latest) {
            return null;
          }

          const status = proxy.alive
            ? 'working'
            : proxy.responseTime === 0
              ? 'timeout'
              : 'failed';

          const entry: ProxyCheck = {
            id: `#${proxy.id}`,
            ip: `${proxy.ip}:${proxy.port}`,
            status,
            date: latest,
            time: this.toTimeLabel(latest)
          };

          if (proxy.responseTime > 0) {
            entry.latency = proxy.responseTime;
          }

          return entry;
        })
        .filter((entry): entry is ProxyCheck => entry !== null)
        .sort((a, b) => b.date.getTime() - a.date.getTime())
        .slice(0, 8)
    );
  }

  private updateAnonymitySummary(entries: JudgeValidProxy[]): void {
    const totals = entries.reduce(
      (acc, entry) => {
        acc.elite += entry.eliteProxies;
        acc.anonymous += entry.anonymousProxies;
        acc.transparent += entry.transparentProxies;
        return acc;
      },
      { elite: 0, anonymous: 0, transparent: 0 }
    );

    const total = totals.elite + totals.anonymous + totals.transparent;

    const segmentConfig: Array<{
      key: keyof typeof totals;
      name: string;
      barClass: string;
      dotColor: string;
    }> = [
      { key: 'elite', name: 'Elite', barClass: 'bg-blue-500/70', dotColor: '#60a5fa' },
      { key: 'anonymous', name: 'Anonymous', barClass: 'bg-orange-500/70', dotColor: '#f59e0b' },
      { key: 'transparent', name: 'Transparent', barClass: 'bg-slate-300/70', dotColor: '#cbd5e1' }
    ];

    this.anonymitySummary.set({ total, change: 0 });
    this.anonymitySegments.set(
      segmentConfig.map((config) => {
        const count = totals[config.key];
        return {
          name: config.name,
          count,
          change: 0,
          share: total > 0 ? count / total : 0,
          barClass: config.barClass,
          dotColor: config.dotColor
        };
      })
    );
  }

  private updateJudgeBreakdown(entries: JudgeValidProxy[]): void {
    const data: Record<string, number> = {};
    entries.forEach((entry) => {
      const total = entry.eliteProxies + entry.anonymousProxies + entry.transparentProxies;
      if (total > 0) {
        data[entry.judgeUrl] = total;
      }
    });

    this.judgeTrafficData.set(data);
  }

  private updateReputationOverview(dashboard: DashboardInfo | undefined): void {
    const source = dashboard?.reputationBreakdown;
    const breakdown: ReputationBreakdown = {
      good: source?.good ?? 0,
      neutral: source?.neutral ?? 0,
      poor: source?.poor ?? 0,
      unknown: source?.unknown ?? 0
    };

    this.reputationBreakdown.set(breakdown);

    const values = [breakdown.good, breakdown.neutral, breakdown.poor, breakdown.unknown];
    const labels = ['Good', 'Neutral', 'Poor', 'Unknown'];
    const accentColors = ['#22c55e', '#f97316', '#ef4444', '#94a3b8'];
    const stemColors = [
      'rgba(34, 197, 94, 0.35)',
      'rgba(249, 115, 22, 0.35)',
      'rgba(239, 68, 68, 0.35)',
      'rgba(148, 163, 184, 0.35)'
    ];

    const lollipopPoints = labels.map((label, index) => ({
      x: values[index],
      y: label
    }));

    this.reputationChartData.set({
      labels,
      datasets: [
        {
          type: 'bar',
          data: values,
          backgroundColor: stemColors,
          borderColor: accentColors,
          borderWidth: 1.25,
          borderRadius: 999,
          borderSkipped: false,
          barThickness: 8,
          maxBarThickness: 12,
          minBarLength: 2,
          hoverBackgroundColor: stemColors,
          hoverBorderColor: accentColors
        },
        {
          type: 'scatter',
          data: lollipopPoints,
          backgroundColor: accentColors,
          borderColor: '#0f172a',
          borderWidth: 2,
          pointRadius: 9,
          pointHoverRadius: 11,
          hitRadius: 24,
          clip: false
        }
      ]
    });

    const total = values.reduce((sum, value) => sum + value, 0);

    this.reputationChartOptions.set({
      responsive: true,
      maintainAspectRatio: false,
      animation: { duration: 650 },
      indexAxis: 'y',
      layout: {
        padding: { left: 8, right: 16, top: 8, bottom: 8 }
      },
      plugins: {
        legend: { display: false },
        tooltip: {
          backgroundColor: '#111827',
          titleColor: '#f9fafb',
          bodyColor: '#e5e7eb',
          borderColor: '#1f2937',
          borderWidth: 1,
          padding: 12,
          callbacks: {
            label: (context: any) => {
              const label = context?.label ?? '';
              const rawValue = context?.raw;
              const parsedValue = context?.parsed;

              let value = 0;
              if (typeof rawValue === 'number') {
                value = rawValue;
              } else if (rawValue && typeof rawValue.x === 'number') {
                value = rawValue.x;
              } else if (typeof parsedValue === 'number') {
                value = parsedValue;
              } else if (parsedValue && typeof parsedValue.x === 'number') {
                value = parsedValue.x;
              } else if (parsedValue && typeof parsedValue.r === 'number') {
                value = parsedValue.r;
              }

              const chartData = context?.chart?.data;
              const datasetIndex = typeof context?.datasetIndex === 'number' ? context.datasetIndex : 0;
              const dataset = chartData?.datasets?.[datasetIndex];
              const dataPoints = Array.isArray(dataset?.data) ? dataset.data : [];

              const datasetTotal = dataPoints.reduce((sum: number, entry: unknown) => {
                return typeof entry === 'number' ? sum + entry : sum;
              }, 0);

              const effectiveTotal = datasetTotal > 0 ? datasetTotal : total;
              const share = effectiveTotal > 0 ? ((value / effectiveTotal) * 100).toFixed(1) : '0.0';
              const formatted = this.numberFormatter.format(value);
              return `${label}: ${formatted} (${share}%)`;
            }
          }
        }
      },
      elements: {
        bar: {
          borderRadius: 999,
          borderSkipped: false
        }
      },
      scales: {
        x: {
          beginAtZero: true,
          grid: {
            color: '#1f2937'
          },
          ticks: {
            color: '#d1d5db',
            callback: (value: string | number) => {
              if (typeof value === 'number') {
                return this.numberFormatter.format(value);
              }
              const parsed = Number(value);
              return Number.isNaN(parsed) ? value : this.numberFormatter.format(parsed);
            }
          },
          border: {
            color: '#1f2937'
          }
        },
        y: {
          type: 'category',
          grid: {
            display: false
          },
          ticks: {
            color: '#94a3b8',
            font: {
              weight: '600'
            }
          }
        }
      }
    });
  }

  private buildProxiesLineChart(history: ProxyHistoryEntry[], limit: number | null | undefined): void {
    const parsed = history
      .map((entry) => {
        const timestamp = this.parseDate(entry.recordedAt);
        if (!timestamp) {
          return null;
        }
        return { timestamp, count: entry.count ?? 0 };
      })
      .filter((entry): entry is { timestamp: Date; count: number } => entry !== null)
      .sort((a, b) => a.timestamp.getTime() - b.timestamp.getTime());

    const effectiveLimit = typeof limit === 'number' && Number.isFinite(limit) && limit >= 0 ? limit : null;

    const labelFormatter = new Intl.DateTimeFormat('en-US', {
      month: 'short',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit'
    });

    if (!parsed.length) {
      this.proxiesLineDiff = { gained: [0], lost: [0] };
      const diffRef = this.proxiesLineDiff;
      const datasets: Array<Record<string, unknown>> = [
        {
          label: this.proxyLineI18n.proxiesLabel,
          data: [0],
          borderColor: '#3b82f6',
          backgroundColor: 'rgba(59, 130, 246, 0.2)',
          fill: true,
          tension: 0.3,
          pointRadius: 4,
          pointHoverRadius: 6
        }
      ];

      if (effectiveLimit !== null) {
        datasets.push({
          label: this.proxyLineI18n.limitLabel,
          data: [effectiveLimit],
          borderColor: '#f59e0b',
          borderDash: [5, 5],
          pointRadius: 0,
          fill: false
        });
      }

      this.proxiesLineData.set({
        labels: [this.proxyLineI18n.noDataLabel],
        datasets
      });
      this.proxiesLineOptions.set(this.createProxyLineOptions(diffRef));
      return;
    }

    const labels = parsed.map((entry) => labelFormatter.format(entry.timestamp));
    const values = parsed.map((entry) => entry.count);

    const gained = values.map((value, index) => (index === 0 ? value : value - values[index - 1]));
    const lost = gained.map((value) => (value < 0 ? Math.abs(value) : 0));
    this.proxiesLineDiff = { gained, lost };

    const datasets: Array<Record<string, unknown>> = [
      {
        label: this.proxyLineI18n.proxiesLabel,
        data: values,
        borderColor: '#3b82f6',
        backgroundColor: 'rgba(59, 130, 246, 0.2)',
        fill: true,
        tension: 0.3,
        pointRadius: 4,
        pointHoverRadius: 6
      }
    ];

    if (effectiveLimit !== null) {
      datasets.push({
        label: this.proxyLineI18n.limitLabel,
        data: values.map(() => effectiveLimit),
        borderColor: '#f59e0b',
        borderDash: [5, 5],
        pointRadius: 0,
        fill: false
      });
    }

    const diffRef = this.proxiesLineDiff;

    this.proxiesLineData.set({
      labels,
      datasets
    });

    this.proxiesLineOptions.set(this.createProxyLineOptions(diffRef));
  }

  private createProxyLineOptions(diffRef: { gained: number[]; lost: number[] }) {
    return {
      responsive: true,
      maintainAspectRatio: false,
      plugins: {
        tooltip: {
          callbacks: {
            label: (context: any) => {
              const index = context.dataIndex;
              const value = context.dataset.data[index];
              if (context.datasetIndex === 0) {
                const gainedValue = diffRef.gained[index] ?? 0;
                const lostValue = diffRef.lost[index] ?? 0;
                return `${this.proxyLineI18n.tooltipProxiesLabel}: ${this.formatChartValue(value)} (${this.proxyLineI18n.tooltipGainedLabel}: ${this.formatChartValue(Math.max(gainedValue, 0))}, ${this.proxyLineI18n.tooltipLostLabel}: ${this.formatChartValue(lostValue)})`;
              }
              return `${this.proxyLineI18n.tooltipLimitLabel}: ${this.formatChartValue(value)}`;
            }
          }
        },
        legend: {
          labels: { color: '#e5e7eb' }
        }
      },
      scales: {
        x: {
          ticks: { color: '#9ca3af' },
          grid: { color: '#374151' }
        },
        y: {
          ticks: {
            color: '#9ca3af',
            callback: (value: string | number) => this.formatChartValue(value)
          },
          grid: { color: '#374151' }
        }
      }
    };
  }

  private parseDate(raw?: string): Date | null {
    if (!raw) {
      return null;
    }
    const date = new Date(raw);
    return Number.isNaN(date.getTime()) ? null : date;
  }

  private toTimeLabel(date: Date): string {
    return new Intl.DateTimeFormat(this.localeId, {
      hour: '2-digit',
      minute: '2-digit'
    }).format(date);
  }

  private formatChartValue(value: string | number): string {
    if (typeof value === 'number') {
      return this.numberFormatter.format(value);
    }

    const parsed = Number(value);
    return Number.isNaN(parsed) ? value : this.numberFormatter.format(parsed);
  }

  private resolveProxyLineI18n(_localeId: string): ProxyLineI18n {
    return {
      title: 'Proxies per Hour (Last 7 Days)',
      proxiesLabel: 'Proxies',
      limitLabel: 'Proxy Limit',
      noDataLabel: 'No Data',
      tooltipProxiesLabel: 'Proxies',
      tooltipGainedLabel: 'Gained',
      tooltipLostLabel: 'Lost',
      tooltipLimitLabel: 'Limit'
    };
  }
}
