export type ScrapeSourceListColumnId =
  | 'url'
  | 'proxy_count'
  | 'alive_count'
  | 'health'
  | 'robots_check'
  | 'scrape_now'
  | 'actions';

export interface ScrapeSourceListColumnDefinition {
  id: ScrapeSourceListColumnId;
  label: string;
  sortField?: string;
  example?: string;
  skeletonWidth?: string;
}

export const SCRAPE_SOURCE_LIST_COLUMN_DEFINITIONS: readonly ScrapeSourceListColumnDefinition[] = [
  {
    id: 'url',
    label: 'URL',
    sortField: 'url',
    example: 'https://source.example/list',
    skeletonWidth: '100%',
  },
  {
    id: 'proxy_count',
    label: 'Proxy Count',
    sortField: 'proxy_count',
    example: '12,345',
    skeletonWidth: '3rem',
  },
  {
    id: 'alive_count',
    label: 'Alive Proxies',
    sortField: 'alive_count',
    example: '8,742',
    skeletonWidth: '3rem',
  },
  {
    id: 'health',
    label: 'Health',
    sortField: 'health',
    example: '82% alive',
    skeletonWidth: '7rem',
  },
  {
    id: 'robots_check',
    label: 'Robots Check',
    example: 'Check robots.txt',
    skeletonWidth: '7.5rem',
  },
  {
    id: 'scrape_now',
    label: 'Scrape Now',
    example: 'Queue',
    skeletonWidth: '5rem',
  },
  {
    id: 'actions',
    label: 'Actions',
    example: 'Open',
    skeletonWidth: '4.5rem',
  },
];

export const DEFAULT_SCRAPE_SOURCE_LIST_COLUMNS: readonly ScrapeSourceListColumnId[] = [
  'url',
  'proxy_count',
  'health',
  'robots_check',
  'actions',
];

const columnById = new Map<ScrapeSourceListColumnId, ScrapeSourceListColumnDefinition>(
  SCRAPE_SOURCE_LIST_COLUMN_DEFINITIONS.map(column => [column.id, column] as const)
);

export function getScrapeSourceListColumnDefinition(id: ScrapeSourceListColumnId): ScrapeSourceListColumnDefinition {
  return columnById.get(id) ?? columnById.get('url')!;
}

export function normalizeScrapeSourceListColumns(value: unknown): ScrapeSourceListColumnId[] {
  if (!Array.isArray(value)) {
    return [...DEFAULT_SCRAPE_SOURCE_LIST_COLUMNS];
  }

  const seen = new Set<ScrapeSourceListColumnId>();
  const normalized: ScrapeSourceListColumnId[] = [];

  for (const item of value) {
    if (typeof item !== 'string') {
      continue;
    }
    const legacyMapped = item === 'details' ? 'actions' : item;
    const candidate = legacyMapped as ScrapeSourceListColumnId;
    if (!columnById.has(candidate) || seen.has(candidate)) {
      continue;
    }
    seen.add(candidate);
    normalized.push(candidate);
  }

  if (normalized.length === 0) {
    return [...DEFAULT_SCRAPE_SOURCE_LIST_COLUMNS];
  }

  return normalized;
}
