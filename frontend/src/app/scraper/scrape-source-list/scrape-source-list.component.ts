import {
  Component,
  ElementRef,
  EventEmitter,
  HostListener,
  OnDestroy,
  OnInit,
  Output,
  signal,
  ViewChild
} from '@angular/core';
import {CommonModule} from '@angular/common';
import {FormBuilder, FormGroup, FormsModule} from '@angular/forms';
import {SelectionModel} from '@angular/cdk/collections';
import {Router} from '@angular/router';
import {HttpService} from '../../services/http.service';
import {ScrapeSourceInfo} from '../../models/ScrapeSourceInfo';
import {ScrapeSourceListFilters} from '../../models/ScrapeSourceListFilters';
import {AddScrapeSourceComponent} from '../add-scrape-source/add-scrape-source.component';
import {ExportSourcesComponent} from './export-sources/export-sources.component';
import {DeleteSourcesComponent} from './delete-sources/delete-sources.component';
import {ScrapeSourceFilterPanelComponent} from './scrape-source-filter-panel/scrape-source-filter-panel.component';

// PrimeNG imports
import {TableLazyLoadEvent, TableModule} from 'primeng/table';
import {ButtonModule} from 'primeng/button';
import {CheckboxModule} from 'primeng/checkbox';
import {SkeletonModule} from 'primeng/skeleton';
import {Tooltip} from 'primeng/tooltip';
import {NotificationService} from '../../services/notification-service.service';
import {SettingsService} from '../../services/settings.service';
import {UserSettings} from '../../models/UserSettings';
import {UserService} from '../../services/authorization/user.service';
import {
  DEFAULT_SCRAPE_SOURCE_LIST_COLUMNS,
  getScrapeSourceListColumnDefinition,
  normalizeScrapeSourceListColumns,
  SCRAPE_SOURCE_LIST_COLUMN_DEFINITIONS,
  ScrapeSourceListColumnDefinition,
  ScrapeSourceListColumnId
} from './scrape-source-list-columns';
import {filter, finalize} from 'rxjs/operators';
import {Subscription} from 'rxjs';
import {HealthBarCellComponent} from '../../shared/health-bar-cell/health-bar-cell.component';
import {ColumnPickerPanelComponent} from '../../shared/column-picker-panel/column-picker-panel.component';

interface ScrapeSourceView extends ScrapeSourceInfo {
  urlHead: string;
  urlTail: string;
}

type ScrapeSourceFilterFormValues = {
  http: boolean;
  https: boolean;
  proxyCountOperator: '<' | '>';
  proxyCount: number;
  aliveCountOperator: '<' | '>';
  aliveCount: number;
};

type ScrapeSourceAppliedFilters = {
  protocols: string[];
  proxyCountOperator: '<' | '>';
  proxyCount: number;
  aliveCountOperator: '<' | '>';
  aliveCount: number;
};
type PageScrollTarget = 'top' | 'bottom';

@Component({
  selector: 'app-scrape-source-list',
  imports: [
    CommonModule,
    FormsModule,
    TableModule,
    ButtonModule,
    CheckboxModule,
    SkeletonModule,
    Tooltip,
    AddScrapeSourceComponent,
    ExportSourcesComponent,
    DeleteSourcesComponent,
    ScrapeSourceFilterPanelComponent,
    HealthBarCellComponent,
    ColumnPickerPanelComponent,
  ],
  templateUrl: './scrape-source-list.component.html',
  styleUrl: './scrape-source-list.component.scss',
  standalone: true
})
export class ScrapeSourceListComponent implements OnInit, OnDestroy {
  private static nextPageJumpInputId = 0;
  private readonly pageScrollTargetStorageKey = 'magpie-scrape-source-list-page-scroll-target';
  private readonly pageSizeStorageKey = 'magpie-scrape-source-list-page-size';

  @Output() showAddScrapeSourceMessage = new EventEmitter<boolean>();
  @ViewChild('filterToggleAnchor') private filterToggleAnchor?: ElementRef<HTMLElement>;
  @ViewChild('filterPanelRef') private filterPanelRef?: ElementRef<HTMLElement>;
  @ViewChild('columnToggleAnchor') private columnToggleAnchor?: ElementRef<HTMLElement>;
  @ViewChild('columnPanelRef', { read: ElementRef }) private columnPanelRef?: ElementRef<HTMLElement>;
  @ViewChild('scrapeSourceTableRoot', { read: ElementRef }) private scrapeSourceTableRoot?: ElementRef<HTMLElement>;

  scrapeSources: ScrapeSourceView[] = [];
  selection = new SelectionModel<ScrapeSourceView>(true, []);
  selectedScrapeSources: ScrapeSourceView[] = [];
  page = 0; // PrimeNG uses 0-based pagination
  pageJumpValue = 1;
  pageScrollTarget: PageScrollTarget = 'top';
  pageSize = 40;
  totalItems = 0;
  hasLoaded = false;
  loading = false;
  searchTerm = '';
  checkingRobots: Record<number, boolean> = {};
  scrapingSources: Record<number, boolean> = {};
  respectRobotsEnabled = false;
  filterPanelOpen = false;
  columnPanelOpen = false;
  sortField: string | null = null;
  sortOrder: number | null = null;
  isAdmin = UserService.isAdmin();
  isSavingColumnPreferences = signal(false);
  displayedColumns: ScrapeSourceListColumnId[] = [...DEFAULT_SCRAPE_SOURCE_LIST_COLUMNS];
  readonly skeletonRows = Array.from({ length: 6 });
  readonly rowsPerPageOptions = [20, 40, 60, 100];
  readonly defaultScrapeSourceColumns = DEFAULT_SCRAPE_SOURCE_LIST_COLUMNS;
  readonly scrapeSourceColumnDefinitions = SCRAPE_SOURCE_LIST_COLUMN_DEFINITIONS;
  readonly pageJumpInputId = `scrape-source-page-jump-${ScrapeSourceListComponent.nextPageJumpInputId++}`;
  readonly pageJumpTotalId = `${this.pageJumpInputId}-total`;
  columnPickerColumns: readonly ScrapeSourceListColumnDefinition[] = this.resolveColumnPickerColumns();
  filterForm: FormGroup;
  appliedFilters: ScrapeSourceAppliedFilters = this.createDefaultAppliedFilters();

  private subscriptions = new Subscription();
  private suppressOutsideCloseUntil = 0;
  private searchDebounceHandle?: ReturnType<typeof setTimeout>;
  private readonly defaultFilterValues: ScrapeSourceFilterFormValues = this.createDefaultFilterValues();
  private pendingPageScroll = false;

  constructor(
    private http: HttpService,
    private router: Router,
    private notification: NotificationService,
    private settingsService: SettingsService,
    private userService: UserService,
    private fb: FormBuilder,
  ) {
    this.filterForm = this.fb.group({
      http: [this.defaultFilterValues.http],
      https: [this.defaultFilterValues.https],
      proxyCountOperator: [this.defaultFilterValues.proxyCountOperator],
      proxyCount: [this.defaultFilterValues.proxyCount],
      aliveCountOperator: [this.defaultFilterValues.aliveCountOperator],
      aliveCount: [this.defaultFilterValues.aliveCount],
    });
  }

  ngOnInit(): void {
    const storedPageScrollTarget = this.getStoredPageScrollTarget();
    if (storedPageScrollTarget) {
      this.pageScrollTarget = storedPageScrollTarget;
    }
    const storedPageSize = this.getStoredPageSize();
    if (storedPageSize !== null) {
      this.pageSize = storedPageSize;
    }

    this.columnPickerColumns = this.resolveColumnPickerColumns();
    this.syncColumnsFromSettings(this.settingsService.getUserSettings());
    const settingsSub = this.settingsService.userSettings$
      .pipe(filter((settings): settings is UserSettings => !!settings))
      .subscribe(settings => this.syncColumnsFromSettings(settings));
    const roleSub = this.userService.role$.subscribe(role => {
      if (role === undefined) {
        return;
      }

      this.isAdmin = role === 'admin';
      this.columnPickerColumns = this.resolveColumnPickerColumns();
      this.syncColumnsFromSettings(this.settingsService.getUserSettings());
      if (!this.isAdmin) {
        this.scrapingSources = {};
      }
    });
    this.subscriptions.add(settingsSub);
    this.subscriptions.add(roleSub);

    this.loadRespectRobotsSetting();
    this.getAndSetScrapeSourceCount();
    this.getAndSetScrapeSourcesList();
  }

  ngOnDestroy(): void {
    this.subscriptions.unsubscribe();
    if (this.searchDebounceHandle) {
      clearTimeout(this.searchDebounceHandle);
    }
  }

  @HostListener('document:click', ['$event'])
  onDocumentClick(event: MouseEvent): void {
    if (Date.now() < this.suppressOutsideCloseUntil) {
      return;
    }

    const target = event.target as Node | null;
    if (!target) {
      return;
    }

    if (
      this.filterPanelOpen &&
      !this.isTargetWithin(target, this.filterToggleAnchor, this.filterPanelRef) &&
      !this.isTargetWithinScrapeSourceFilterOverlay(target)
    ) {
      this.filterPanelOpen = false;
    }

    if (this.columnPanelOpen && !this.isTargetWithin(target, this.columnToggleAnchor, this.columnPanelRef)) {
      this.columnPanelOpen = false;
    }
  }

  private loadRespectRobotsSetting(): void {
    this.http.getRespectRobotsSetting().subscribe({
      next: res => {
        this.respectRobotsEnabled = res?.respect_robots_txt;
      },
      error: err => {
        this.notification.showWarn('Could not load robots.txt setting: ' + (err?.error?.error ?? err?.message ?? 'Unknown error'));
        this.respectRobotsEnabled = false;
      }
    });
  }

  getAndSetScrapeSourcesList() {
    this.loading = true;
    const trimmedSearch = this.searchTerm.trim();
    this.http.getScrapingSourcePage(this.page + 1, {
      rows: this.pageSize,
      search: trimmedSearch.length > 0 ? trimmedSearch : undefined,
      filters: this.buildFilterPayload(this.appliedFilters),
    }).subscribe({
      next: res => {
        const sources = Array.isArray(res) ? res : [];
        this.scrapeSources = this.applySort(
          sources.map(source => this.buildViewSource(source)),
          this.sortField,
          this.sortOrder
        );
        this.syncSelectionWithData();
        this.loading = false;
        this.hasLoaded = true;
        this.applyPendingPageScroll();
        const shouldShowEmptyState = this.totalItems === 0 && sources.length === 0;
        this.showAddScrapeSourceMessage.emit(shouldShowEmptyState);
      },
      error: err => {
        this.notification.showError("Could not get scraping sources" + err.error.message);
        this.loading = false;
        this.hasLoaded = true;
        this.applyPendingPageScroll();
        this.showAddScrapeSourceMessage.emit(false);
      }
    });
  }

  getAndSetScrapeSourceCount() {
    const trimmedSearch = this.searchTerm.trim();
    this.http.getScrapingSourcesCount({
      search: trimmedSearch.length > 0 ? trimmedSearch : undefined,
      filters: this.buildFilterPayload(this.appliedFilters),
    }).subscribe({
      next: res => {
        this.totalItems = res ?? 0;
        const shouldShowEmptyState = this.totalItems === 0 && this.hasLoaded && this.scrapeSources.length === 0;
        this.showAddScrapeSourceMessage.emit(shouldShowEmptyState);
      },
      error: err => {
        this.notification.showError("Could not get scrape sources count " + err.error.message);
      }
    });
  }

  onLazyLoad(event: TableLazyLoadEvent) {
    const requestedRows = event.rows ?? this.pageSize;
    const newPageSize = Number.isFinite(requestedRows) && requestedRows > 0 ? requestedRows : this.pageSize;
    const newPage = Math.floor((event.first ?? 0) / newPageSize);
    const nextSortOrder = event.sortOrder && event.sortOrder !== 0 ? event.sortOrder : null;
    const nextSortField = nextSortOrder ? this.resolveSortField(event.sortField) : null;

    const sortChanged = nextSortField !== this.sortField || nextSortOrder !== this.sortOrder;
    const pageSizeChanged = newPageSize !== this.pageSize;
    const shouldFetch = newPage !== this.page || pageSizeChanged;

    this.page = newPage;
    this.pageJumpValue = newPage + 1;
    this.pageSize = newPageSize;
    this.sortField = nextSortField;
    this.sortOrder = nextSortOrder;

    if (pageSizeChanged) {
      this.persistPageSize(newPageSize);
    }

    if (sortChanged) {
      this.scrapeSources = this.applySort([...this.scrapeSources], this.sortField, this.sortOrder);
      this.syncSelectionWithData();
    }

    if (shouldFetch) {
      this.pendingPageScroll = true;
      this.getAndSetScrapeSourcesList();
    }
  }

  get totalPages(): number {
    if (!Number.isFinite(this.totalItems) || !Number.isFinite(this.pageSize) || this.pageSize <= 0) {
      return 1;
    }

    return Math.max(1, Math.ceil(this.totalItems / this.pageSize));
  }

  get pageScrollTargetIcon(): string {
    return this.pageScrollTarget === 'top' ? 'pi-arrow-up' : 'pi-arrow-down';
  }

  get pageScrollTargetLabel(): string {
    return this.pageScrollTarget === 'top'
      ? 'Page changes scroll to top'
      : 'Page changes stay at bottom';
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
      this.pageJumpValue = this.page + 1;
      return;
    }

    const nextPage = Math.min(this.totalPages, Math.max(1, Math.floor(rawPage)));
    this.pageJumpValue = nextPage;

    if (nextPage === this.page + 1) {
      return;
    }

    this.onLazyLoad({
      first: (nextPage - 1) * this.pageSize,
      rows: this.pageSize,
      sortField: this.sortField,
      sortOrder: this.sortOrder,
    });
  }

  // Helper method to get selection count
  getSelectionCount(): number {
    return this.selection.selected.length;
  }

  toggleSelection(source: ScrapeSourceView): void {
    this.selection.toggle(source);
    this.selectedScrapeSources = [...this.selection.selected];
  }

  isAllSelected(): boolean {
    return this.scrapeSources.length > 0 && this.selection.selected.length === this.scrapeSources.length;
  }

  isSomeSelected(): boolean {
    const count = this.selection.selected.length;
    return count > 0 && count < this.scrapeSources.length;
  }

  masterToggle(): void {
    if (this.isAllSelected()) {
      this.selection.clear();
    } else {
      this.scrapeSources.forEach(source => this.selection.select(source));
    }
    this.selectedScrapeSources = [...this.selection.selected];
  }

  refreshList(): void {
    this.selection.clear();
    this.selectedScrapeSources = [];
    this.getAndSetScrapeSourceCount();
    this.getAndSetScrapeSourcesList();
  }

  onSearchTermChange(value: string): void {
    if (this.searchDebounceHandle) {
      clearTimeout(this.searchDebounceHandle);
    }

    this.searchTerm = value;
    this.searchDebounceHandle = setTimeout(() => {
      this.resetPaginatorToFirstPage();
      this.refreshList();
    }, 300);
  }

  openColumnPanel(event?: Event | { originalEvent?: Event }): void {
    this.stopTriggerEvent(event);
    if (this.columnPanelOpen) {
      this.columnPanelOpen = false;
      return;
    }
    this.suppressOutsideCloseUntil = Date.now() + 180;
    this.filterPanelOpen = false;
    this.columnPanelOpen = true;
  }

  closeColumnPanel(): void {
    this.columnPanelOpen = false;
  }

  toggleFilterPanel(event?: Event | { originalEvent?: Event }): void {
    this.stopTriggerEvent(event);
    const nextState = !this.filterPanelOpen;
    if (nextState) {
      this.syncFilterFormWithApplied();
      this.suppressOutsideCloseUntil = Date.now() + 180;
      this.columnPanelOpen = false;
    }
    this.filterPanelOpen = nextState;
  }

  applyFilters(): void {
    this.appliedFilters = this.buildFiltersFromForm();
    this.resetPaginatorToFirstPage();
    this.refreshList();
    this.filterPanelOpen = false;
  }

  clearFilters(): void {
    this.filterForm.reset(this.defaultFilterValues);
    this.appliedFilters = this.createDefaultAppliedFilters();
    this.resetPaginatorToFirstPage();
    this.refreshList();
  }

  hasActiveFilters(): boolean {
    return this.activeFilterCount() > 0;
  }

  filterButtonLabel(): string {
    const count = this.activeFilterCount();
    return count > 0 ? `Filters (${count})` : 'Filters';
  }

  filterToggleClass(): string {
    return this.hasActiveFilters()
      ? 'p-button-outlined filter-toggle filter-toggle--active'
      : 'p-button-outlined filter-toggle';
  }

  onColumnEditorDragStart(): void {
    this.suppressOutsideCloseUntil = Date.now() + 60_000;
  }

  onColumnEditorDragEnd(): void {
    this.suppressOutsideCloseUntil = Date.now() + 240;
  }

  saveColumnPreferences(nextColumns: string[]): void {
    const previous = [...this.displayedColumns];
    const next = normalizeScrapeSourceListColumns(nextColumns);

    this.displayedColumns = next;
    this.columnPanelOpen = false;
    this.isSavingColumnPreferences.set(true);

    this.settingsService.saveScrapeSourceListColumns(next)
      .pipe(finalize(() => { this.isSavingColumnPreferences.set(false); }))
      .subscribe({
        error: err => {
          this.displayedColumns = previous;
          const message = err?.error?.message ?? err?.message ?? 'Unknown error';
          this.notification.showError('Could not save column settings: ' + message);
        }
      });
  }

  tableColumns(): ScrapeSourceListColumnDefinition[] {
    return this.displayedColumns
      .map(column => getScrapeSourceListColumnDefinition(column))
      .filter(column => this.isAdmin || column.id !== 'scrape_now')
      .filter(column => this.respectRobotsEnabled || column.id !== 'robots_check');
  }

  tableColumnCount(): number {
    return this.tableColumns().length + 1;
  }

  trackByColumn(_index: number, column: ScrapeSourceListColumnDefinition): ScrapeSourceListColumnId {
    return column.id;
  }

  onScrapeSourcesAdded(): void {
    this.resetPaginatorToFirstPage();
    this.refreshList();
  }

  onScrapeSourcesDeleted(): void {
    this.resetPaginatorToFirstPage();
    this.refreshList();
  }

  onShowAddScrapeSourcesMessage(value: boolean): void {
    this.showAddScrapeSourceMessage.emit(value);
  }

  onViewSource(event: Event | { originalEvent?: Event }, source: ScrapeSourceView): void {
    if ((event as { originalEvent?: Event }).originalEvent) {
      (event as { originalEvent?: Event }).originalEvent?.stopPropagation?.();
    } else {
      (event as Event)?.stopPropagation?.();
    }
    this.router.navigate(['/scraper', source.id]).catch(() => {});
  }

  checkRobots(source: ScrapeSourceView, event?: Event): void {
    event?.stopPropagation();
    if (!source?.url) {
      return;
    }

    this.checkingRobots[source.id] = true;
    this.http.checkScrapeSource(source.url).subscribe({
      next: res => {
        const { allowed, robots_found, error } = res ?? { allowed: true, robots_found: false };

        if (allowed && robots_found) {
          this.notification.showSuccess('robots.txt allows scraping this URL');
        } else if (!allowed && robots_found) {
          this.notification.showWarn('robots.txt disallows scraping this URL');
        } else if (allowed && !robots_found) {
          this.notification.showInfo('No robots.txt found; scraping is allowed by default');
        }

        if (error) {
          this.notification.showWarn('Robots check completed with warnings: ' + error);
        }
      },
      error: err => {
        this.notification.showError('Could not check robots.txt: ' + (err?.error?.error ?? err?.message ?? 'Unknown error'));
      }
    }).add(() => {
      delete this.checkingRobots[source.id];
    });
  }

  isCheckingRobots(sourceId: number): boolean {
    return this.checkingRobots[sourceId];
  }

  isScrapingSource(sourceId: number): boolean {
    return this.scrapingSources[sourceId];
  }

  scrapeSourceNow(source: ScrapeSourceView, event?: Event): void {
    event?.stopPropagation();
    if (!this.isAdmin || !source?.id) {
      return;
    }

    this.scrapingSources[source.id] = true;
    this.http.requeueScrapeSource(source.id).subscribe({
      next: res => {
        this.notification.showSuccess(res?.message ?? 'Scrape source queued successfully');
      },
      error: err => {
        this.notification.showError('Could not queue scrape source: ' + (err?.error?.error ?? err?.message ?? 'Unknown error'));
      }
    }).add(() => {
      delete this.scrapingSources[source.id];
    });
  }

  private buildViewSource(source: ScrapeSourceInfo): ScrapeSourceView {
    const { head, tail } = this.splitUrlForDisplay(source.url);
    return {
      ...source,
      urlHead: head,
      urlTail: tail,
    };
  }

  private resetPaginatorToFirstPage(): void {
    this.page = 0;
    this.pageJumpValue = 1;
  }

  private applyPendingPageScroll(): void {
    if (!this.pendingPageScroll) {
      return;
    }

    this.pendingPageScroll = false;
    setTimeout(() => this.scrollToPageTarget(), 0);
  }

  private getStoredPageScrollTarget(): PageScrollTarget | null {
    try {
      const storage = this.getStorage();
      if (!storage) {
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
      if (!storage) {
        return;
      }

      storage.setItem(this.pageScrollTargetStorageKey, target);
    } catch {
      // ignore persistence errors (private browsing, SSR)
    }
  }

  private getStoredPageSize(): number | null {
    try {
      const storage = this.getStorage();
      if (!storage) {
        return null;
      }
      const raw = storage.getItem(this.pageSizeStorageKey);
      if (!raw) {
        return null;
      }
      const parsed = Number(raw);
      if (!Number.isFinite(parsed) || parsed <= 0) {
        return null;
      }
      if (!this.rowsPerPageOptions.includes(parsed)) {
        return null;
      }
      return parsed;
    } catch {
      return null;
    }
  }

  private persistPageSize(size: number): void {
    if (!Number.isFinite(size) || size <= 0) {
      return;
    }
    try {
      const storage = this.getStorage();
      if (!storage) {
        return;
      }
      storage.setItem(this.pageSizeStorageKey, size.toString());
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
    const root = this.scrapeSourceTableRoot?.nativeElement;
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

  private createDefaultFilterValues(): ScrapeSourceFilterFormValues {
    return {
      http: false,
      https: false,
      proxyCountOperator: '>',
      proxyCount: 0,
      aliveCountOperator: '>',
      aliveCount: 0,
    };
  }

  private createDefaultAppliedFilters(): ScrapeSourceAppliedFilters {
    return {
      protocols: [],
      proxyCountOperator: '>',
      proxyCount: 0,
      aliveCountOperator: '>',
      aliveCount: 0,
    };
  }

  private syncFilterFormWithApplied(): void {
    this.filterForm.patchValue({
      http: this.appliedFilters.protocols.includes('http'),
      https: this.appliedFilters.protocols.includes('https'),
      proxyCountOperator: this.appliedFilters.proxyCountOperator,
      proxyCount: this.appliedFilters.proxyCount,
      aliveCountOperator: this.appliedFilters.aliveCountOperator,
      aliveCount: this.appliedFilters.aliveCount,
    }, { emitEvent: false });
  }

  private buildFiltersFromForm(): ScrapeSourceAppliedFilters {
    const formValue = this.filterForm.getRawValue() as ScrapeSourceFilterFormValues;
    const protocols: string[] = [];
    if (formValue.http) {
      protocols.push('http');
    }
    if (formValue.https) {
      protocols.push('https');
    }

    return {
      protocols,
      proxyCountOperator: formValue.proxyCountOperator === '<' ? '<' : '>',
      proxyCount: this.normalizeFilterNumber(formValue.proxyCount),
      aliveCountOperator: formValue.aliveCountOperator === '<' ? '<' : '>',
      aliveCount: this.normalizeFilterNumber(formValue.aliveCount),
    };
  }

  private buildFilterPayload(filters: ScrapeSourceAppliedFilters): ScrapeSourceListFilters | undefined {
    const payload: ScrapeSourceListFilters = {};
    if (filters.protocols.length > 0) {
      payload.protocols = filters.protocols;
    }
    if (filters.proxyCount > 0) {
      payload.proxyCountOperator = filters.proxyCountOperator;
      payload.proxyCount = filters.proxyCount;
    }
    if (filters.aliveCount > 0) {
      payload.aliveCountOperator = filters.aliveCountOperator;
      payload.aliveCount = filters.aliveCount;
    }
    return Object.keys(payload).length > 0 ? payload : undefined;
  }

  private activeFilterCount(): number {
    let count = 0;
    if (this.appliedFilters.protocols.length > 0) {
      count += 1;
    }
    if (this.appliedFilters.proxyCount > 0) {
      count += 1;
    }
    if (this.appliedFilters.aliveCount > 0) {
      count += 1;
    }
    return count;
  }

  private normalizeFilterNumber(value: number | string | null | undefined): number {
    if (value === null || value === undefined) {
      return 0;
    }
    const parsed = typeof value === 'string' ? Number(value) : value;
    if (!Number.isFinite(parsed)) {
      return 0;
    }
    return Math.max(0, Math.floor(parsed));
  }

  private applySort(
    sources: ScrapeSourceView[],
    field: string | null,
    order: number | null,
  ): ScrapeSourceView[] {
    if (!field || !order || sources.length < 2) {
      return sources;
    }

    const direction = order > 0 ? 1 : -1;
    return [...sources].sort((left, right) => this.compareSources(left, right, field, direction));
  }

  private compareSources(
    left: ScrapeSourceView,
    right: ScrapeSourceView,
    field: string,
    direction: number,
  ): number {
    const leftValue = this.getSortableValue(left, field);
    const rightValue = this.getSortableValue(right, field);

    if (typeof leftValue === 'number' && typeof rightValue === 'number') {
      if (leftValue === rightValue) {
        return left.url.localeCompare(right.url);
      }
      return (leftValue - rightValue) * direction;
    }

    const comparison = String(leftValue).localeCompare(String(rightValue), undefined, {
      numeric: true,
      sensitivity: 'base',
    });
    if (comparison === 0) {
      return left.url.localeCompare(right.url) * direction;
    }
    return comparison * direction;
  }

  private getSortableValue(source: ScrapeSourceView, field: string): number | string {
    switch (field) {
      case 'proxy_count':
        return source.proxy_count ?? 0;
      case 'alive_count':
        return source.alive_count ?? 0;
      case 'health':
        return this.calculateHealthScore(source);
      case 'url':
      default:
        return source.url ?? '';
    }
  }

  private calculateHealthScore(source: ScrapeSourceView): number {
    const total = source.proxy_count ?? 0;
    if (total <= 0) {
      return -1;
    }

    const alive = source.alive_count ?? 0;
    return alive / total;
  }

  private resolveSortField(field: string | string[] | undefined | null): string | null {
    if (typeof field === 'string' && field.trim().length > 0) {
      return field;
    }

    if (Array.isArray(field)) {
      const first = field.find(value => value.trim().length > 0);
      return first ?? null;
    }

    return null;
  }

  private splitUrlForDisplay(url: string | null | undefined): { head: string; tail: string } {
    const safeUrl = (url ?? '').trim();
    if (!safeUrl) {
      return { head: '', tail: '' };
    }

    const minLengthForSplit = 36;
    if (safeUrl.length <= minLengthForSplit) {
      return { head: safeUrl, tail: '' };
    }

    const queryIndex = safeUrl.indexOf('?');
    if (queryIndex >= 0 && queryIndex < safeUrl.length - 1) {
      const base = safeUrl.slice(0, queryIndex);
      const query = safeUrl.slice(queryIndex + 1);
      const queryTailLength = 24;

      if (query.length > queryTailLength) {
        return {
          head: `${base}?${query.slice(0, query.length - queryTailLength)}`,
          tail: query.slice(-queryTailLength)
        };
      }
    }

    const trimmed = safeUrl.endsWith('/') ? safeUrl.slice(0, -1) : safeUrl;
    const schemeIndex = trimmed.indexOf('://');
    const hostStart = schemeIndex >= 0 ? schemeIndex + 3 : 0;
    const lastSlash = trimmed.lastIndexOf('/');

    if (lastSlash > hostStart && lastSlash < trimmed.length - 1) {
      const head = trimmed.slice(0, lastSlash);
      const tail = trimmed.slice(lastSlash) + (safeUrl.endsWith('/') ? '/' : '');
      return { head, tail };
    }

    const fallbackTailLength = 12;
    if (safeUrl.length <= fallbackTailLength) {
      return { head: safeUrl, tail: '' };
    }

    return {
      head: safeUrl.slice(0, safeUrl.length - fallbackTailLength),
      tail: safeUrl.slice(-fallbackTailLength)
    };
  }

  private syncSelectionWithData(): void {
    const selectedIds = new Set(this.selection.selected.map(source => source.id));
    this.selection.clear();

    this.scrapeSources.forEach(source => {
      if (selectedIds.has(source.id)) {
        this.selection.select(source);
      }
    });
    this.selectedScrapeSources = [...this.selection.selected];
  }

  private syncColumnsFromSettings(settings: UserSettings | undefined): void {
    this.displayedColumns = normalizeScrapeSourceListColumns(settings?.scrape_source_list_columns ?? DEFAULT_SCRAPE_SOURCE_LIST_COLUMNS);
  }

  private resolveColumnPickerColumns(): readonly ScrapeSourceListColumnDefinition[] {
    if (this.isAdmin) {
      return this.scrapeSourceColumnDefinitions;
    }
    return this.scrapeSourceColumnDefinitions.filter(column => column.id !== 'scrape_now');
  }

  private isTargetWithin(target: Node, ...elements: Array<ElementRef<HTMLElement> | undefined>): boolean {
    for (const elementRef of elements) {
      const element = elementRef?.nativeElement;
      if (element && element.contains(target)) {
        return true;
      }
    }
    return false;
  }

  private isTargetWithinScrapeSourceFilterOverlay(target: Node): boolean {
    const element = target instanceof Element ? target : target.parentElement;
    return !!element?.closest('.scrape-source-filter-panel__overlay');
  }

  private stopTriggerEvent(event?: Event | { originalEvent?: Event }): void {
    if (!event) {
      return;
    }
    const domEvent = (event as { originalEvent?: Event }).originalEvent ?? (event as Event);
    domEvent?.stopPropagation?.();
  }
}
