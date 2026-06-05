import {
  ChangeDetectionStrategy,
  ChangeDetectorRef,
  Component,
  ElementRef,
  EventEmitter,
  Input,
  OnChanges,
  OnDestroy,
  OnInit,
  Output,
  SimpleChanges,
  ViewChild
} from '@angular/core';
import {NgClass} from '@angular/common';
import {SelectionModel} from '@angular/cdk/collections';
import {Table, TableLazyLoadEvent, TableModule} from 'primeng/table';
import {SkeletonModule} from 'primeng/skeleton';
import {Tooltip} from 'primeng/tooltip';
import {CheckboxModule} from 'primeng/checkbox';
import {FormsModule} from '@angular/forms';
import {ProxyInfo} from '../../models/ProxyInfo';
import {ProxyReputation} from '../../models/ProxyReputation';
import {ClipboardService} from '../../services/clipboard.service';
import {HealthBarCellComponent} from '../health-bar-cell/health-bar-cell.component';
import {
  DEFAULT_PROXY_TABLE_COLUMNS,
  ProxyTableColumnDefinition,
  ProxyTableColumnId,
  getProxyTableColumnDefinition,
  normalizeProxyTableColumns,
} from './proxy-table-columns';

interface ProxyRowMeta {
  hasReputation: boolean;
  reputationBadgeClass: string;
  reputationLabel: string;
  reputationScore: string;
  latestCheckLabel: string;
}

type ProxyRow = ProxyInfo & { __meta?: ProxyRowMeta };
type PageScrollTarget = 'top' | 'bottom';

@Component({
  selector: 'app-proxy-table',
  standalone: true,
  imports: [
    NgClass,
    FormsModule,
    TableModule,
    CheckboxModule,
    SkeletonModule,
    Tooltip,
    HealthBarCellComponent,
  ],
  templateUrl: './proxy-table.component.html',
  styleUrls: ['./proxy-table.component.scss'],
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class ProxyTableComponent implements OnInit, OnChanges, OnDestroy {
  @ViewChild('tableRoot') private table?: Table;
  @ViewChild('tableRoot', { read: ElementRef }) private tableRoot?: ElementRef<HTMLElement>;

  private _proxies: ProxyRow[] = [];
  private _columns: ProxyTableColumnId[] = [...DEFAULT_PROXY_TABLE_COLUMNS];
  private static nextPageJumpInputId = 0;
  private readonly dateFormatter = new Intl.DateTimeFormat(undefined, {
    dateStyle: 'short',
    timeStyle: 'short',
  });

  @Input()
  set proxies(value: ProxyInfo[]) {
    this._proxies = (value ?? []) as ProxyRow[];
    this.decorateProxies();
  }
  get proxies(): ProxyRow[] {
    return this._proxies;
  }
  @Input()
  set columns(value: ProxyTableColumnId[]) {
    this._columns = normalizeProxyTableColumns(value);
  }
  get columns(): ProxyTableColumnId[] {
    return this._columns;
  }
  @Input() loading = false;
  @Input() hasLoaded = false;
  @Input() page = 1;
  @Input() pageSize = 20;
  @Input() totalRecords = 0;
  @Input() rowsPerPageOptions: number[] = [20, 50];
  @Input() skeletonRows: unknown[] = [];

  @Input() selectionEnabled = false;
  @Input() selection: ProxyInfo[] = [];
  @Input() selectionModel?: SelectionModel<ProxyInfo>;
  @Input() isAllSelected = false;

  @Input() sortable = false;
  @Input() sortField: string | null = null;
  @Input() sortOrder: number | null = null;
  @Input() checkingProxyIds: Record<number, boolean> = {};

  @Input() emptyReputationLabel = '—';
  @Input() missingReputationScoreLabel: string | null = null;
  @Input() virtualScroll = false;
  @Input() virtualScrollItemSize = 46;
  @Input() scrollHeight: string | null = null;
  @Input() pageScrollTargetStorageKey: string | null = null;

  @Output() lazyLoad = new EventEmitter<TableLazyLoadEvent>();
  @Output() selectionChange = new EventEmitter<ProxyInfo[]>();
  @Output() toggleSelection = new EventEmitter<ProxyInfo>();
  @Output() masterToggle = new EventEmitter<void>();
  @Output() sort = new EventEmitter<{ field: string; order: number }>();
  @Output() checkNow = new EventEmitter<ProxyInfo>();
  @Output() viewProxy = new EventEmitter<ProxyInfo>();

  copiedValueKey: string | null = null;
  pageJumpValue = this.page;
  pageScrollTarget: PageScrollTarget = 'top';
  readonly pageJumpInputId = `proxy-table-page-jump-${ProxyTableComponent.nextPageJumpInputId++}`;
  readonly pageJumpTotalId = `${this.pageJumpInputId}-total`;
  private copyFeedbackTimeout?: ReturnType<typeof setTimeout>;
  private pendingPageScroll = false;

  constructor(
    private cdr: ChangeDetectorRef,
    private clipboardService: ClipboardService,
  ) {}

  ngOnInit(): void {
    const storedPageScrollTarget = this.getStoredPageScrollTarget();
    if (storedPageScrollTarget) {
      this.pageScrollTarget = storedPageScrollTarget;
    }
  }

  ngOnChanges(changes: SimpleChanges): void {
    if (changes['page']) {
      this.pageJumpValue = this.page;
      if (!changes['page'].firstChange) {
        this.pendingPageScroll = true;
        if (!this.loading) {
          this.applyPendingPageScroll();
        }
      }
    }

    if (changes['loading'] && changes['loading'].previousValue === true && !this.loading) {
      this.applyPendingPageScroll();
    }

    if (changes['emptyReputationLabel'] || changes['missingReputationScoreLabel']) {
      this.decorateProxies();
    }

    if (
      (changes['sortField'] || changes['sortOrder']) &&
      this.sortable &&
      (!this.sortField || !this.sortOrder)
    ) {
      this.resetPrimeNgSortVisualState();
    }
  }

  ngOnDestroy(): void {
    if (this.copyFeedbackTimeout) {
      clearTimeout(this.copyFeedbackTimeout);
    }
  }

  get columnCount(): number {
    return this.visibleColumns.length + (this.selectionEnabled ? 1 : 0);
  }

  get visibleColumns(): ProxyTableColumnDefinition[] {
    return this.columns.map(column => getProxyTableColumnDefinition(column));
  }

  get scoreFallback(): string {
    return this.missingReputationScoreLabel ?? this.emptyReputationLabel;
  }

  get totalPages(): number {
    if (!Number.isFinite(this.totalRecords) || !Number.isFinite(this.pageSize) || this.pageSize <= 0) {
      return 1;
    }

    return Math.max(1, Math.ceil(this.totalRecords / this.pageSize));
  }

  get pageScrollTargetIcon(): string {
    return this.pageScrollTarget === 'top' ? 'pi-arrow-up' : 'pi-arrow-down';
  }

  get pageScrollTargetLabel(): string {
    return this.pageScrollTarget === 'top'
      ? 'Page changes scroll to top'
      : 'Page changes stay at bottom';
  }

  trackByProxy(_index: number, proxy: ProxyRow): number {
    return proxy.id;
  }

  onSelectionChange(value: ProxyInfo[]): void {
    if (!this.selectionEnabled) {
      return;
    }
    this.selectionChange.emit(value);
  }

  onMasterToggle(): void {
    if (!this.selectionEnabled) {
      return;
    }
    this.masterToggle.emit();
  }

  isSomeSelected(): boolean {
    if (!this.selectionEnabled || !this.selectionModel) {
      return false;
    }
    const count = this.selectionModel.selected.length;
    return count > 0 && !this.isAllSelected;
  }

  onToggleSelection(proxy: ProxyInfo): void {
    if (!this.selectionEnabled) {
      return;
    }
    this.toggleSelection.emit(proxy);
  }

  onSort(event: { field: string; order: number }): void {
    if (!this.sortable) {
      return;
    }
    this.sort.emit(event);
  }

  onPageJumpSubmit(event: Event): void {
    event.preventDefault();
    this.commitPageJump();
  }

  togglePageScrollTarget(): void {
    this.pageScrollTarget = this.pageScrollTarget === 'top' ? 'bottom' : 'top';
    this.persistPageScrollTarget(this.pageScrollTarget);
  }

  commitPageJump(): void {
    const rawPage = Number(this.pageJumpValue);
    if (!Number.isFinite(rawPage)) {
      this.pageJumpValue = this.page;
      return;
    }

    const nextPage = Math.min(this.totalPages, Math.max(1, Math.floor(rawPage)));
    this.pageJumpValue = nextPage;

    if (nextPage === this.page) {
      return;
    }

    this.lazyLoad.emit({
      first: (nextPage - 1) * this.pageSize,
      rows: this.pageSize,
      sortField: this.sortField,
      sortOrder: this.sortOrder,
    });
  }

  isCheckingProxy(proxyId: number): boolean {
    return this.checkingProxyIds[proxyId];
  }

  onCheckNow(event: Event | { originalEvent?: Event }, proxy: ProxyInfo): void {
    if ((event as { originalEvent?: Event }).originalEvent) {
      (event as { originalEvent?: Event }).originalEvent?.stopPropagation?.();
    } else {
      (event as Event)?.stopPropagation?.();
    }
    this.checkNow.emit(proxy);
  }

  trackByColumn(_index: number, column: ProxyTableColumnDefinition): ProxyTableColumnId {
    return column.id;
  }

  skeletonWidth(column: ProxyTableColumnDefinition): string {
    return column.skeletonWidth ?? '6rem';
  }

  hasHealthValue(value: number | null | undefined): boolean {
    return typeof value === 'number' && Number.isFinite(value);
  }

  healthDeadPercent(value: number | null | undefined): number {
    if (!this.hasHealthValue(value)) {
      return 0;
    }
    return Math.max(0, 100 - (value as number));
  }

  onViewProxy(event: Event | { originalEvent?: Event }, proxy: ProxyInfo): void {
    if ((event as { originalEvent?: Event }).originalEvent) {
      (event as { originalEvent?: Event }).originalEvent?.stopPropagation?.();
    } else {
      (event as Event)?.stopPropagation?.();
    }
    this.viewProxy.emit(proxy);
  }

  isCopied(proxy: ProxyInfo, field: 'ip' | 'ip_port' | 'port'): boolean {
    return this.copiedValueKey === this.getCopyValueKey(proxy, field);
  }

  async copyProxyValue(event: MouseEvent, proxy: ProxyInfo, field: 'ip' | 'ip_port' | 'port'): Promise<void> {
    event.stopPropagation();
    const value = this.resolveCopyValue(proxy, field);
    if (!value) {
      return;
    }
    const key = this.getCopyValueKey(proxy, field);
    this.showCopyFeedback(key);
    await this.clipboardService.copyText(value);
  }

  private decorateProxies(): void {
    if (!this._proxies.length) {
      return;
    }
    for (const proxy of this._proxies) {
      this.buildMeta(proxy);
    }
  }

  private buildMeta(proxy: ProxyRow): ProxyRowMeta {
    const reputation = this.getPrimaryReputation(proxy);
    if (!reputation) {
      proxy.__meta = {
        hasReputation: false,
        reputationBadgeClass: 'reputation-badge reputation-badge--unknown',
        reputationLabel: 'Unknown',
        reputationScore: this.scoreFallback,
        latestCheckLabel: this.formatLatestCheck(proxy.latest_check),
      };
      return proxy.__meta;
    }

    const rawLabel = `${reputation.label ?? ''}`.trim();
    const label = rawLabel.length > 0 ? rawLabel : 'Unknown';
    const normalized = label.toLowerCase();
    let badgeClass = 'reputation-badge reputation-badge--unknown';
    if (normalized === 'good') {
      badgeClass = 'reputation-badge reputation-badge--good';
    } else if (normalized === 'neutral') {
      badgeClass = 'reputation-badge reputation-badge--neutral';
    } else if (normalized === 'poor') {
      badgeClass = 'reputation-badge reputation-badge--poor';
    }

    const score = reputation.score === null || reputation.score === undefined
      ? this.scoreFallback
      : Math.round(reputation.score).toString();

    proxy.__meta = {
      hasReputation: true,
      reputationBadgeClass: badgeClass,
      reputationLabel: label,
      reputationScore: score,
      latestCheckLabel: this.formatLatestCheck(proxy.latest_check),
    };
    return proxy.__meta;
  }

  private formatLatestCheck(value: unknown): string {
    if (!value) {
      return '—';
    }
    const date = value instanceof Date ? value : new Date(value as string);
    if (!Number.isFinite(date.getTime())) {
      return '—';
    }
    return this.dateFormatter.format(date);
  }

  private getPrimaryReputation(proxy: ProxyInfo): ProxyReputation | null {
    const reputation = proxy.reputation;
    if (!reputation) {
      return null;
    }

    if (reputation.overall) {
      return reputation.overall;
    }

    if (reputation.protocols) {
      for (const rep of Object.values(reputation.protocols)) {
        if (rep) {
          return rep;
        }
      }
    }

    return null;
  }

  private resolveCopyValue(proxy: ProxyInfo, field: 'ip' | 'ip_port' | 'port'): string {
    if (field === 'ip_port') {
      return `${proxy.ip}:${proxy.port}`;
    }
    if (field === 'port') {
      return `${proxy.port}`;
    }
    return proxy.ip;
  }

  private getCopyValueKey(proxy: ProxyInfo, field: 'ip' | 'ip_port' | 'port'): string {
    return `${proxy.id}:${field}`;
  }

  private showCopyFeedback(key: string): void {
    this.copiedValueKey = key;
    if (this.copyFeedbackTimeout) {
      clearTimeout(this.copyFeedbackTimeout);
    }
    this.copyFeedbackTimeout = setTimeout(() => {
      if (this.copiedValueKey === key) {
        this.copiedValueKey = null;
        this.cdr.markForCheck();
      }
    }, 1400);
    this.cdr.markForCheck();
  }

  private applyPendingPageScroll(): void {
    if (!this.pendingPageScroll) {
      return;
    }

    this.pendingPageScroll = false;
    setTimeout(() => this.scrollToPageTarget(), 0);
  }

  private resetPrimeNgSortVisualState(): void {
    setTimeout(() => {
      if (!this.table || (this.sortField && this.sortOrder)) {
        return;
      }

      this.table._sortField = null;
      this.table._sortOrder = 0;
      this.table.tableService.onSort(null);
      this.cdr.markForCheck();
    }, 0);
  }

  private getStoredPageScrollTarget(): PageScrollTarget | null {
    try {
      const storage = this.getStorage();
      if (!storage || !this.pageScrollTargetStorageKey) {
        return null;
      }

      const raw = storage.getItem(this.pageScrollTargetStorageKey);
      return this.isPageScrollTarget(raw) ? raw : null;
    } catch {
      return null;
    }
  }

  private persistPageScrollTarget(target: PageScrollTarget): void {
    try {
      const storage = this.getStorage();
      if (!storage || !this.pageScrollTargetStorageKey) {
        return;
      }

      storage.setItem(this.pageScrollTargetStorageKey, target);
    } catch {
      // ignore persistence errors (private browsing, SSR)
    }
  }

  private isPageScrollTarget(value: string | null): value is PageScrollTarget {
    return value === 'top' || value === 'bottom';
  }

  private getStorage(): Storage | null {
    if (typeof window === 'undefined' || !window?.localStorage) {
      return null;
    }

    return window.localStorage;
  }

  private scrollToPageTarget(): void {
    const root = this.tableRoot?.nativeElement;
    if (!root || typeof window === 'undefined') {
      return;
    }

    const innerScroller = root.querySelector<HTMLElement>('.p-datatable-wrapper');
    if (innerScroller && innerScroller.scrollHeight > innerScroller.clientHeight) {
      innerScroller.scrollTo({
        top: this.pageScrollTarget === 'top' ? 0 : innerScroller.scrollHeight,
        behavior: 'auto',
      });
    }

    const scrollContainer = this.getScrollContainer(root);
    if (!scrollContainer) {
      window.scrollTo({
        top: this.pageScrollTarget === 'top'
          ? root.getBoundingClientRect().top + window.scrollY
          : root.getBoundingClientRect().bottom + window.scrollY - window.innerHeight,
        left: 0,
        behavior: 'auto',
      });
      return;
    }

    const rootRect = root.getBoundingClientRect();
    const containerRect = scrollContainer.getBoundingClientRect();
    const currentTop = scrollContainer.scrollTop;
    const targetTop = this.pageScrollTarget === 'top'
      ? currentTop + rootRect.top - containerRect.top
      : currentTop + rootRect.bottom - containerRect.bottom;

    scrollContainer.scrollTo({
      top: Math.max(0, targetTop),
      behavior: 'auto',
    });
  }

  private getScrollContainer(start: HTMLElement): HTMLElement | null {
    let parent = start.parentElement;
    while (parent) {
      const style = window.getComputedStyle(parent);
      const overflowY = style.overflowY;
      const canScroll = /(auto|scroll)/.test(overflowY) && parent.scrollHeight > parent.clientHeight;
      if (canScroll) {
        return parent;
      }
      parent = parent.parentElement;
    }
    return null;
  }

}
