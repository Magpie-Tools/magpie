import {ComponentFixture, TestBed} from '@angular/core/testing';
import {MessageService} from 'primeng/api';
import {BehaviorSubject, of} from 'rxjs';
import {RouterTestingModule} from '@angular/router/testing';
import {ProxyListComponent} from './proxy-list.component';
import {HttpService} from '../../services/http.service';
import {SettingsService} from '../../services/settings.service';
import {UserService} from '../../services/authorization/user.service';

describe('ProxyListComponent', () => {
  let component: ProxyListComponent;
  let fixture: ComponentFixture<ProxyListComponent>;
  let httpServiceStub: {
    getProxyPage: jasmine.Spy;
    getProxyFilterOptions: jasmine.Spy;
    requeueProxy: jasmine.Spy;
  };

  beforeEach(async () => {
    httpServiceStub = {
      getProxyPage: jasmine.createSpy('getProxyPage').and.returnValue(of({proxies: [], total: 0})),
      getProxyFilterOptions: jasmine.createSpy('getProxyFilterOptions').and.returnValue(of({countries: [], types: [], anonymityLevels: []})),
      requeueProxy: jasmine.createSpy('requeueProxy').and.returnValue(of({message: 'Proxy queued successfully', proxy_id: 1})),
    };
    const settingsServiceStub = {
      getUserSettings: jasmine.createSpy('getUserSettings').and.returnValue(undefined),
      userSettings$: of(undefined),
      saveProxyListColumns: jasmine.createSpy('saveProxyListColumns').and.returnValue(of({message: 'ok'})),
    } satisfies Partial<SettingsService>;
    const role$ = new BehaviorSubject<string | undefined>('user');
    const userServiceStub = {
      role$: role$.asObservable(),
    } satisfies Partial<UserService>;

    await TestBed.configureTestingModule({
      imports: [ProxyListComponent, RouterTestingModule],
      providers: [
        MessageService,
        {provide: HttpService, useValue: httpServiceStub},
        {provide: SettingsService, useValue: settingsServiceStub},
        {provide: UserService, useValue: userServiceStub},
      ]
    })
    .compileComponents();

    fixture = TestBed.createComponent(ProxyListComponent);
    component = fixture.componentInstance;
    fixture.detectChanges();
  });

  it('should create', () => {
    expect(component).toBeTruthy();
  });

  it('keeps the filter panel open when clicking an appended filter select overlay', () => {
    const overlay = document.createElement('div');
    const option = document.createElement('button');
    overlay.className = 'proxy-filter-panel__overlay';
    overlay.appendChild(option);
    document.body.appendChild(overlay);
    component.filterPanelOpen.set(true);

    option.dispatchEvent(new MouseEvent('click', {bubbles: true}));

    expect(component.filterPanelOpen()).toBeTrue();
    overlay.remove();
  });

  it('closes the filter panel when clicking outside the panel and filter overlays', () => {
    component.filterPanelOpen.set(true);

    document.body.dispatchEvent(new MouseEvent('click', {bubbles: true}));

    expect(component.filterPanelOpen()).toBeFalse();
  });

  it('closes the column panel when opening the filter panel', () => {
    component.columnPanelOpen.set(true);

    component.toggleFilterPanel();

    expect(component.filterPanelOpen()).toBeTrue();
    expect(component.columnPanelOpen()).toBeFalse();
  });

  it('closes the filter panel when opening the column panel', () => {
    component.filterPanelOpen.set(true);

    component.openColumnPanel();

    expect(component.columnPanelOpen()).toBeTrue();
    expect(component.filterPanelOpen()).toBeFalse();
  });

  it('reloads the first page with server sort parameters when sorting a column', () => {
    httpServiceStub.getProxyPage.calls.reset();
    component.page.set(3);
    component.pageSize.set(40);

    component.onSort({field: 'reputation', order: -1});

    expect(component.page()).toBe(1);
    expect(component.sortField()).toBe('reputation');
    expect(component.sortOrder()).toBe(-1);
    expect(httpServiceStub.getProxyPage).toHaveBeenCalledWith(1, jasmine.objectContaining({
      rows: 40,
      sortField: 'reputation',
      sortOrder: -1,
    }));
  });

  it('clears the sort on the third click of the same column', () => {
    component.sortField.set('reputation');
    component.sortOrder.set(-1);
    httpServiceStub.getProxyPage.calls.reset();

    component.onLazyLoad({first: 0, rows: 40, sortField: 'reputation', sortOrder: 1});
    component.onSort({field: 'reputation', order: 1});

    expect(component.sortField()).toBeNull();
    expect(component.sortOrder()).toBeNull();
    expect(httpServiceStub.getProxyPage).toHaveBeenCalledWith(1, jasmine.objectContaining({
      sortField: null,
      sortOrder: null,
    }));
  });
});
