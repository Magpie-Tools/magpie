import {ComponentFixture, TestBed} from '@angular/core/testing';
import {MessageService} from 'primeng/api';
import {RouterTestingModule} from '@angular/router/testing';
import {BehaviorSubject, of} from 'rxjs';
import {ScrapeSourceListComponent} from './scrape-source-list.component';
import {HttpService} from '../../services/http.service';
import {SettingsService} from '../../services/settings.service';
import {UserService} from '../../services/authorization/user.service';

describe('ScrapeSourceListComponent', () => {
  const pageSizeStorageKey = 'magpie-scrape-source-list-page-size';
  let component: ScrapeSourceListComponent;
  let fixture: ComponentFixture<ScrapeSourceListComponent>;

  afterEach(() => {
    window.localStorage.removeItem(pageSizeStorageKey);
  });

  beforeEach(async () => {
    window.localStorage.removeItem(pageSizeStorageKey);

    const httpServiceStub = {
      getRespectRobotsSetting: jasmine.createSpy('getRespectRobotsSetting').and.returnValue(of({respect_robots_txt: false})),
      getScrapingSourcesCount: jasmine.createSpy('getScrapingSourcesCount').and.returnValue(of(0)),
      getScrapingSourcePage: jasmine.createSpy('getScrapingSourcePage').and.returnValue(of([])),
      exportScrapeSources: jasmine.createSpy('exportScrapeSources').and.returnValue(of('')),
      deleteScrapingSource: jasmine.createSpy('deleteScrapingSource').and.returnValue(of('')),
      requeueScrapeSource: jasmine.createSpy('requeueScrapeSource').and.returnValue(of({message: 'Scrape source queued successfully', source_id: 1})),
    } satisfies Partial<HttpService>;
    const settingsServiceStub = {
      getUserSettings: jasmine.createSpy('getUserSettings').and.returnValue(undefined),
      userSettings$: of(undefined),
      saveScrapeSourceListColumns: jasmine.createSpy('saveScrapeSourceListColumns').and.returnValue(of({message: 'ok'})),
    } satisfies Partial<SettingsService>;
    const userRoleSubject = new BehaviorSubject<string | undefined>('user');
    const userServiceStub = {
      role$: userRoleSubject.asObservable(),
    } satisfies Partial<UserService>;

    await TestBed.configureTestingModule({
      imports: [ScrapeSourceListComponent, RouterTestingModule],
      providers: [
        MessageService,
        {provide: HttpService, useValue: httpServiceStub},
        {provide: SettingsService, useValue: settingsServiceStub},
        {provide: UserService, useValue: userServiceStub},
      ]
    })
    .compileComponents();

    fixture = TestBed.createComponent(ScrapeSourceListComponent);
    component = fixture.componentInstance;
    fixture.detectChanges();
  });

  it('should create', () => {
    expect(component).toBeTruthy();
  });

  it('persists table row count changes', () => {
    component.pageSize = 40;

    component.onLazyLoad({first: 0, rows: 60});

    expect(window.localStorage.getItem(pageSizeStorageKey)).toBe('60');
  });
});
