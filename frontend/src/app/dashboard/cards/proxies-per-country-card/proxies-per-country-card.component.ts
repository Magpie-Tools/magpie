import {AfterViewInit, Component, Input, OnChanges, SimpleChanges, ViewChild} from '@angular/core';
import {DecimalPipe} from '@angular/common';
import {FormsModule} from '@angular/forms';
import {Card} from 'primeng/card';
import {PrimeTemplate} from 'primeng/api';
import {UIChart} from 'primeng/chart';
import {Dialog} from 'primeng/dialog';
import Chart from 'chart.js/auto';
import {ChartData, ChartOptions, ChartType, Tooltip, TooltipItem, TooltipPositionerFunction, TooltipPositionerMap} from 'chart.js';
import {ChoroplethController, GeoFeature, ColorScale, ProjectionScale} from 'chartjs-chart-geo';
import {feature} from 'topojson-client';
import worldMap from 'world-atlas/countries-110m.json';
import {Feature, FeatureCollection} from 'geojson';
import {geoMercator} from 'd3-geo';

interface CountryBreakdown {
  name: string;
  percentage: string | number;
  value?: number;
  color?: string;
}

type CountryFeature = Feature & { properties: { name: string } };

Chart.register(ChoroplethController, GeoFeature, ColorScale, ProjectionScale);

const GEO_TOOLTIP_POSITIONER = 'cursorSafe';
const GEO_TOOLTIP_OFFSET = 14;
const GEO_TOOLTIP_MARGIN = 8;

declare module 'chart.js' {
  interface TooltipPositionerMap {
    cursorSafe: TooltipPositionerFunction<ChartType>;
  }
}

const tooltipPositioners = Tooltip.positioners as TooltipPositionerMap;

if (!tooltipPositioners[GEO_TOOLTIP_POSITIONER]) {
  tooltipPositioners[GEO_TOOLTIP_POSITIONER] = function (_items, eventPosition) {
    const chartArea = this.chart.chartArea;
    const width = this.width ?? 0;
    const height = this.height ?? 0;
    const anchorX = eventPosition?.x ?? chartArea.left;
    const anchorY = eventPosition?.y ?? chartArea.top;

    const placeRight = anchorX + GEO_TOOLTIP_OFFSET + width <= chartArea.right - GEO_TOOLTIP_MARGIN;
    const placeBelow = anchorY + GEO_TOOLTIP_OFFSET + height <= chartArea.bottom - GEO_TOOLTIP_MARGIN;

    const nextX = placeRight
      ? anchorX + GEO_TOOLTIP_OFFSET
      : anchorX - width - GEO_TOOLTIP_OFFSET;
    const nextY = placeBelow
      ? anchorY + GEO_TOOLTIP_OFFSET
      : anchorY - height - GEO_TOOLTIP_OFFSET;

    const x = Math.min(
      chartArea.right - width - GEO_TOOLTIP_MARGIN,
      Math.max(chartArea.left + GEO_TOOLTIP_MARGIN, nextX)
    );
    const y = Math.min(
      chartArea.bottom - height - GEO_TOOLTIP_MARGIN,
      Math.max(chartArea.top + GEO_TOOLTIP_MARGIN, nextY)
    );

    return {
      x,
      y,
      xAlign: placeRight ? 'left' : 'right',
      yAlign: placeBelow ? 'top' : 'bottom'
    };
  };
}

const WORLD_TOPO = worldMap as unknown as { objects: { countries: any } };
const WORLD_FEATURE_COLLECTION = feature(
  WORLD_TOPO as any,
  WORLD_TOPO.objects.countries
) as unknown as FeatureCollection;
const WORLD_FEATURES_ALL = WORLD_FEATURE_COLLECTION.features as CountryFeature[];
const WORLD_FEATURES = WORLD_FEATURES_ALL.filter(
  (feat) => (feat.properties?.name ?? '').toLowerCase() !== 'antarctica'
) as CountryFeature[];
const FEATURE_BY_NAME = new Map<string, CountryFeature>();
const WORLD_PROJECTION = geoMercator().rotate([-10, 0]);

WORLD_FEATURES.forEach((feat) => {
  const key = (feat.properties?.name ?? '').toString().toLowerCase();
  if (key) {
    FEATURE_BY_NAME.set(key, feat);
  }
});

const COUNTRY_ALIASES: Record<string, string> = {
  usa: 'United States of America',
  'united states': 'United States of America',
  'united states of america': 'United States of America',
  uk: 'United Kingdom',
  'south korea': 'South Korea',
  'north korea': 'North Korea',
  russia: 'Russia',
  'czech republic': 'Czechia',
  laos: 'Laos',
  'lao people\'s democratic republic': 'Laos',
  vietnam: 'Vietnam',
  venezuela: 'Venezuela',
  bolivia: 'Bolivia',
  tanzania: 'Tanzania',
  syria: 'Syria',
  iran: 'Iran',
  moldova: 'Moldova',
  palestine: 'Palestine',
  'palestinian territories': 'Palestine',
  macedonia: 'Macedonia',
  'north macedonia': 'Macedonia',
  'bosnia and herzegovina': 'Bosnia and Herz.',
  'saint lucia': 'Saint Lucia',
  'st lucia': 'Saint Lucia',
  'st. lucia': 'Saint Lucia',
  'democratic republic of the congo': 'Dem. Rep. Congo',
  'congo (drc)': 'Dem. Rep. Congo',
  'congo drc': 'Dem. Rep. Congo',
  drc: 'Dem. Rep. Congo',
  'republic of the congo': 'Congo',
  'congo republic': 'Congo',
  congo: 'Congo',
  'dr congo': 'Dem. Rep. Congo',
  ivorycoast: "Côte d'Ivoire",
  "cote d'ivoire": "Côte d'Ivoire",
  'ivory coast': "Côte d'Ivoire",
  swaziland: 'eSwatini',
  eswatini: 'eSwatini',
  'the netherlands': 'Netherlands',
  'trinidad and tobago': 'Trinidad and Tobago'
};

Object.entries(COUNTRY_ALIASES).forEach(([alias, canonical]) => {
  const target = FEATURE_BY_NAME.get(canonical.toLowerCase());
  if (target) {
    FEATURE_BY_NAME.set(alias, target);
  }
});

const REGION_CODES = [
  'AD', 'AE', 'AF', 'AG', 'AI', 'AL', 'AM', 'AO', 'AQ', 'AR', 'AS', 'AT', 'AU', 'AW', 'AX', 'AZ',
  'BA', 'BB', 'BD', 'BE', 'BF', 'BG', 'BH', 'BI', 'BJ', 'BL', 'BM', 'BN', 'BO', 'BQ', 'BR', 'BS',
  'BT', 'BV', 'BW', 'BY', 'BZ', 'CA', 'CC', 'CD', 'CF', 'CG', 'CH', 'CI', 'CK', 'CL', 'CM', 'CN',
  'CO', 'CR', 'CU', 'CV', 'CW', 'CX', 'CY', 'CZ', 'DE', 'DJ', 'DK', 'DM', 'DO', 'DZ', 'EC', 'EE',
  'EG', 'EH', 'ER', 'ES', 'ET', 'FI', 'FJ', 'FK', 'FM', 'FO', 'FR', 'GA', 'GB', 'GD', 'GE', 'GF',
  'GG', 'GH', 'GI', 'GL', 'GM', 'GN', 'GP', 'GQ', 'GR', 'GS', 'GT', 'GU', 'GW', 'GY', 'HK', 'HM',
  'HN', 'HR', 'HT', 'HU', 'ID', 'IE', 'IL', 'IM', 'IN', 'IO', 'IQ', 'IR', 'IS', 'IT', 'JE', 'JM',
  'JO', 'JP', 'KE', 'KG', 'KH', 'KI', 'KM', 'KN', 'KP', 'KR', 'KW', 'KY', 'KZ', 'LA', 'LB', 'LC',
  'LI', 'LK', 'LR', 'LS', 'LT', 'LU', 'LV', 'LY', 'MA', 'MC', 'MD', 'ME', 'MF', 'MG', 'MH', 'MK',
  'ML', 'MM', 'MN', 'MO', 'MP', 'MQ', 'MR', 'MS', 'MT', 'MU', 'MV', 'MW', 'MX', 'MY', 'MZ', 'NA',
  'NC', 'NE', 'NF', 'NG', 'NI', 'NL', 'NO', 'NP', 'NR', 'NU', 'NZ', 'OM', 'PA', 'PE', 'PF', 'PG',
  'PH', 'PK', 'PL', 'PM', 'PN', 'PR', 'PS', 'PT', 'PW', 'PY', 'QA', 'RE', 'RO', 'RS', 'RU', 'RW',
  'SA', 'SB', 'SC', 'SD', 'SE', 'SG', 'SH', 'SI', 'SJ', 'SK', 'SL', 'SM', 'SN', 'SO', 'SR', 'SS',
  'ST', 'SV', 'SX', 'SY', 'SZ', 'TC', 'TD', 'TF', 'TG', 'TH', 'TJ', 'TK', 'TL', 'TM', 'TN', 'TO',
  'TR', 'TT', 'TV', 'TW', 'TZ', 'UA', 'UG', 'UM', 'US', 'UY', 'UZ', 'VA', 'VC', 'VE', 'VG', 'VI',
  'VN', 'VU', 'WF', 'WS', 'XK', 'YE', 'YT', 'ZA', 'ZM', 'ZW'
];

const COUNTRY_CODE_OVERRIDES: Record<string, string> = {
  usa: 'US',
  'united states': 'US',
  'united states of america': 'US',
  uk: 'GB',
  'united kingdom': 'GB',
  'south korea': 'KR',
  'north korea': 'KP',
  russia: 'RU',
  'czech republic': 'CZ',
  laos: 'LA',
  vietnam: 'VN',
  venezuela: 'VE',
  bolivia: 'BO',
  tanzania: 'TZ',
  syria: 'SY',
  iran: 'IR',
  moldova: 'MD',
  palestine: 'PS',
  'palestinian territories': 'PS',
  macedonia: 'MK',
  'north macedonia': 'MK',
  'bosnia and herzegovina': 'BA',
  'saint lucia': 'LC',
  'st lucia': 'LC',
  'st. lucia': 'LC',
  'democratic republic of the congo': 'CD',
  'congo (drc)': 'CD',
  'congo drc': 'CD',
  drc: 'CD',
  'republic of the congo': 'CG',
  'congo republic': 'CG',
  congo: 'CG',
  'dr congo': 'CD',
  ivorycoast: 'CI',
  "cote d'ivoire": 'CI',
  'ivory coast': 'CI',
  swaziland: 'SZ',
  eswatini: 'SZ',
  'the netherlands': 'NL',
  'trinidad and tobago': 'TT'
};

@Component({
  selector: 'app-proxies-per-country-card',
  standalone: true,
  imports: [Card, PrimeTemplate, UIChart, Dialog, FormsModule],
  providers: [DecimalPipe],
  templateUrl: './proxies-per-country-card.component.html',
  styleUrl: './proxies-per-country-card.component.scss'
})
export class ProxiesPerCountryCardComponent implements OnChanges, AfterViewInit {
  @Input() title = 'Proxies per country';
  @Input() countries: CountryBreakdown[] = [];
  @Input() styleClass = 'chart-card bg-neutral-900 border border-neutral-800 mb-4';

  viewMode: 'map' | 'countries' = 'map';
  mapData: ChartData<'choropleth'> = { labels: [], datasets: [] };
  mapOptions: ChartOptions<'choropleth'> = {};
  maxCountryValue = 1;
  totalValue = 0;
  readonly mapChartType: any = 'choropleth';
  readonly listLimit = 7;
  showAllCountries = false;
  searchTerm = '';
  private refreshQueued = false;
  private readonly decimalPipe: DecimalPipe;

  @ViewChild('mapChart') mapChart?: UIChart;

  constructor(decimalPipe: DecimalPipe) {
    this.decimalPipe = decimalPipe;
  }

  private readonly regionNames = typeof Intl.DisplayNames !== 'undefined'
    ? new Intl.DisplayNames(['en'], { type: 'region' })
    : null;
  private readonly regionLookup = this.buildRegionLookup();

  ngOnChanges(changes: SimpleChanges): void {
    if (changes['countries']) {
      this.recalculateTotals();
      this.buildMap();
    }
  }

  ngAfterViewInit(): void {
    this.scheduleMapRefresh();
  }

  countryFlag(country: CountryBreakdown): string {
    const code = this.resolveCountryCode(country?.name);
    const emoji = code ? this.toFlagEmoji(code) : undefined;
    return emoji ?? '🌐';
  }

  visibleCountries(): CountryBreakdown[] {
    if (!Array.isArray(this.countries)) {
      return [];
    }
    return this.countries.slice(0, this.listLimit);
  }

  hasMoreCountries(): boolean {
    return (this.countries?.length ?? 0) > this.listLimit;
  }

  filteredCountries(): CountryBreakdown[] {
    const term = this.searchTerm.trim().toLowerCase();
    const entries = this.countries ?? [];
    if (!term) {
      return entries;
    }
    return entries.filter((entry) => (entry.name ?? '').toLowerCase().includes(term));
  }

  setViewMode(mode: 'map' | 'countries'): void {
    this.viewMode = mode;
    if (mode === 'map') {
      this.scheduleMapRefresh();
    }
  }

  countryPercent(country: CountryBreakdown): number {
    const percent = this.resolvePercentage(country);
    if (percent !== undefined) {
      return percent;
    }

    if (this.totalValue <= 0) {
      return 0;
    }

    const value = this.resolveValue(country);
    if (value <= 0) {
      return 0;
    }

    const normalized = (value / this.totalValue) * 100;
    return this.clampPercentage(normalized);
  }

  private buildRegionLookup(): Map<string, string> {
    if (!this.regionNames) {
      return new Map<string, string>();
    }

    const entries: Array<[string, string]> = [];

    for (const code of REGION_CODES) {
      let name: string | undefined;
      try {
        name = this.regionNames.of(code);
      } catch {
        continue;
      }

      if (!name) {
        continue;
      }

      const lower = name.toLowerCase();
      entries.push([lower, code]);

      const normalized = this.normalizeNameKey(name);
      if (normalized && normalized !== lower) {
        entries.push([normalized, code]);
      }
    }

    return new Map(entries);
  }

  private resolveCountryCode(name: string | undefined | null): string | undefined {
    const normalized = (name ?? '').trim();
    if (!normalized) {
      return undefined;
    }

    const lower = normalized.toLowerCase();
    if (lower === 'unknown' || lower === 'others' || lower === 'n/a') {
      return undefined;
    }

    if (/^[a-z]{2}$/i.test(normalized)) {
      return normalized.toUpperCase();
    }

    const override = COUNTRY_CODE_OVERRIDES[lower];
    if (override) {
      return override;
    }

    const lookup = this.regionLookup.get(lower);
    if (lookup) {
      return lookup;
    }

    const compact = this.normalizeNameKey(normalized);
    if (compact) {
      const compactMatch = this.regionLookup.get(compact);
      if (compactMatch) {
        return compactMatch;
      }
    }

    const alias = COUNTRY_ALIASES[lower];
    if (alias) {
      const aliasKey = alias.toLowerCase();
      const aliasMatch = this.regionLookup.get(aliasKey)
        ?? this.regionLookup.get(this.normalizeNameKey(aliasKey));
      if (aliasMatch) {
        return aliasMatch;
      }
    }

    for (const [key, code] of this.regionLookup.entries()) {
      if (key.startsWith(lower) || lower.startsWith(key)) {
        return code;
      }
      if (compact && (key.startsWith(compact) || compact.startsWith(key))) {
        return code;
      }
    }

    return undefined;
  }

  private normalizeNameKey(value: string): string {
    return value
      .toLowerCase()
      .normalize('NFKD')
      .replace(/[\u0300-\u036f]/g, '')
      .replace(/[^a-z]/g, '');
  }

  private toFlagEmoji(code: string): string | undefined {
    if (!code || code.length !== 2 || !/^[A-Z]{2}$/i.test(code)) {
      return undefined;
    }

    const upper = code.toUpperCase();
    const base = 0x1f1e6;
    const offset = (char: string) => base + (char.charCodeAt(0) - 65);
    return String.fromCodePoint(offset(upper[0]), offset(upper[1]));
  }

  private buildMap(): void {
    const values = new Map<string, number>();
    let maxValue = 0;

    for (const entry of this.countries ?? []) {
      const featureMatch = this.resolveFeature(entry.name);
      if (!featureMatch) {
        continue;
      }

      const value = this.resolveValue(entry);
      if (value <= 0) {
        continue;
      }

      const key = (featureMatch.properties?.name ?? '').toLowerCase();
      const nextValue = (values.get(key) ?? 0) + value;
      values.set(key, nextValue);
      maxValue = Math.max(maxValue, nextValue);
    }

    this.maxCountryValue = Math.max(maxValue, 1);

    const dataset = WORLD_FEATURES.map((feature) => {
      const key = (feature.properties?.name ?? '').toLowerCase();
      return {
        feature,
        value: values.get(key) ?? 0
      };
    });

    this.mapData = {
      labels: WORLD_FEATURES.map((feat) => feat.properties?.name ?? ''),
      datasets: [
        {
          label: 'Proxies',
          outline: WORLD_FEATURES as any[],
          data: dataset,
          borderColor: 'rgba(255, 255, 255, 0.18)',
          borderWidth: 1.1,
          hoverBorderColor: 'rgba(255, 255, 255, 0.85)',
          hoverBorderWidth: 1.1,
          hoverBackgroundColor: (context: any) => {
            const raw = context.raw as { value?: number } | undefined;
            const value = typeof raw?.value === 'number' ? raw.value : 0;
            const normalized = this.maxCountryValue > 0 ? value / this.maxCountryValue : 0;
            return this.interpolateColor(Math.min(1, normalized + 0.25));
          }
        }
      ]
    };

    this.mapOptions = this.createMapOptions(this.maxCountryValue);
    this.scheduleMapRefresh();
  }

  private scheduleMapRefresh(): void {
    if (this.refreshQueued) {
      return;
    }
    this.refreshQueued = true;

    const finalize = () => {
      this.refreshQueued = false;
      this.mapChart?.refresh();
    };

    if (typeof requestAnimationFrame !== 'undefined') {
      requestAnimationFrame(() => requestAnimationFrame(finalize));
    } else {
      setTimeout(finalize, 0);
    }
  }

  private resolveFeature(name: string | undefined | null): CountryFeature | undefined {
    const normalized = (name ?? '').trim();
    if (!normalized || normalized.toLowerCase() === 'unknown' || normalized.toLowerCase() === 'others') {
      return undefined;
    }

    const lower = normalized.toLowerCase();
    const exact = FEATURE_BY_NAME.get(lower);
    if (exact) {
      return exact;
    }

    const alias = COUNTRY_ALIASES[lower];
    if (alias) {
      const mapped = FEATURE_BY_NAME.get(alias.toLowerCase());
      if (mapped) {
        return mapped;
      }
    }

    if (this.regionNames && /^[a-z]{2,3}$/i.test(normalized)) {
      const display = this.regionNames.of(normalized.toUpperCase());
      if (display) {
        const displayKey = display.toLowerCase();
        const displayMatch = FEATURE_BY_NAME.get(displayKey)
          ?? FEATURE_BY_NAME.get((COUNTRY_ALIASES[displayKey] ?? '').toLowerCase());
        if (displayMatch) {
          return displayMatch;
        }
      }
    }

    return WORLD_FEATURES.find((feat) => {
      const key = (feat.properties?.name ?? '').toString().toLowerCase();
      return key === lower || key.startsWith(lower) || lower.startsWith(key);
    });
  }

  private resolveValue(country: CountryBreakdown): number {
    if (typeof country.value === 'number' && !Number.isNaN(country.value)) {
      return country.value;
    }

    if (typeof country.percentage === 'number' && !Number.isNaN(country.percentage)) {
      return country.percentage;
    }

    const parsed = Number.parseFloat(country.percentage?.toString() ?? '0');
    return Number.isFinite(parsed) ? parsed : 0;
  }

  private resolvePercentage(country: CountryBreakdown): number | undefined {
    if (typeof country?.percentage === 'number' && Number.isFinite(country.percentage)) {
      return this.clampPercentage(country.percentage);
    }

    if (typeof country?.percentage === 'string') {
      const parsed = Number.parseFloat(country.percentage.replace(/[^\d.-]/g, ''));
      if (Number.isFinite(parsed)) {
        return this.clampPercentage(parsed);
      }
    }

    return undefined;
  }

  private clampPercentage(value: number): number {
    if (!Number.isFinite(value)) {
      return 0;
    }
    return Math.min(100, Math.max(0, value));
  }

  private recalculateTotals(): void {
    let sum = 0;

    for (const entry of this.countries ?? []) {
      const value = this.resolveValue(entry);
      if (value > 0) {
        sum += value;
      }
    }

    this.totalValue = sum;
  }

  private createMapOptions(maxValue: number): ChartOptions<'choropleth'> {
    const max = Math.max(maxValue, 1);
    return {
      maintainAspectRatio: false,
      transitions: {
        active: {
          animation: { duration: 0 }
        }
      },
      interaction: {
        mode: 'nearest',
        intersect: true
      },
      plugins: {
        legend: { display: false },
        tooltip: {
          position: GEO_TOOLTIP_POSITIONER,
          animation: false,
          animations: {
            numbers: { duration: 0 },
            opacity: { duration: 0 }
          },
          displayColors: false,
          caretSize: 0,
          caretPadding: 10,
          callbacks: {
            label: (context: TooltipItem<'choropleth'>) => {
              const raw = context.raw as any;
              const feature = raw?.feature as CountryFeature | undefined;
              const label = feature?.properties?.name ?? context.label ?? 'Unknown';
              const value = typeof raw?.value === 'number' ? raw.value : 0;
              return `${label}: ${this.formatNumber(value)}`;
            }
          },
          backgroundColor: '#0b1220',
          titleColor: '#e5e7eb',
          bodyColor: '#cbd5e1',
          borderColor: '#1f2937',
          borderWidth: 1,
          padding: 10
        }
      },
      elements: {
        geoFeature: {
          borderColor: 'rgba(255, 255, 255, 0.2)',
          borderWidth: 1.1,
          hoverBorderColor: 'rgba(255, 255, 255, 0.85)',
          hoverBorderWidth: 1.1,
          graticuleBorderColor: 'rgba(255, 255, 255, 0.08)',
          graticuleBorderWidth: 0.6
        }
      },
      showOutline: true,
      showGraticule: false,
      scales: {
        projection: {
          axis: 'x',
          projection: WORLD_PROJECTION
        },
        color: {
          axis: 'x',
          min: 0,
          max,
          quantize: 8,
          missing: '#0c1424',
          display: false,
          ticks: { display: false },
          interpolate: (value: number) => this.interpolateColor(value)
        }
      }
    };
  }

  private formatNumber(value: number): string {
    return this.decimalPipe.transform(value, '1.0-0') ?? '0';
  }

  private interpolateColor(value: number): string {
    const clamped = Math.min(1, Math.max(0, value));
    const start = [9, 12, 24];
    const end = [180, 225, 255];
    const channel = (index: number) => Math.round(start[index] + (end[index] - start[index]) * clamped);
    const alpha = 0.55 + clamped * 0.4;
    return `rgba(${channel(0)}, ${channel(1)}, ${channel(2)}, ${alpha})`;
  }
}
