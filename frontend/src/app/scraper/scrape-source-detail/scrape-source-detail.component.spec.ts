import {ComponentFixture, TestBed} from '@angular/core/testing';
import {MessageService} from 'primeng/api';
import {ActivatedRoute, convertToParamMap} from '@angular/router';
import {RouterTestingModule} from '@angular/router/testing';
import {of} from 'rxjs';
import {ScrapeSourceDetailComponent} from './scrape-source-detail.component';
import {ScrapeSourceDetail} from '../../models/ScrapeSourceDetail';
import {HttpService} from '../../services/http.service';

describe('ScrapeSourceDetailComponent', () => {
  let component: ScrapeSourceDetailComponent;
  let fixture: ComponentFixture<ScrapeSourceDetailComponent>;
  let httpServiceStub: {
    getScrapeSourceDetail: jasmine.Spy;
    getScrapeSourceProxyPage: jasmine.Spy;
    getProxyFilterOptions: jasmine.Spy;
  };

  beforeEach(async () => {
    const detail: ScrapeSourceDetail = {
      id: 1,
      url: 'https://example.com',
      added_at: new Date().toISOString(),
      proxy_count: 24,
      alive_count: 14,
      dead_count: 6,
      unknown_count: 4,
      avg_reputation: 68,
      last_proxy_added_at: new Date().toISOString(),
      last_checked_at: new Date().toISOString(),
      reputation_breakdown: {
        good: 8,
        neutral: 7,
        poor: 5,
        unknown: 4,
      }
    };

    httpServiceStub = {
      getScrapeSourceDetail: jasmine.createSpy('getScrapeSourceDetail').and.returnValue(of(detail)),
      getScrapeSourceProxyPage: jasmine.createSpy('getScrapeSourceProxyPage').and.returnValue(of({ proxies: [], total: 0 })),
      getProxyFilterOptions: jasmine.createSpy('getProxyFilterOptions').and.returnValue(of({countries: [], types: [], anonymityLevels: []})),
    };

    await TestBed.configureTestingModule({
      imports: [ScrapeSourceDetailComponent, RouterTestingModule],
      providers: [
        MessageService,
        {provide: HttpService, useValue: httpServiceStub},
        {
          provide: ActivatedRoute,
          useValue: {
            paramMap: of(convertToParamMap({id: '1'})),
          }
        }
      ]
    }).compileComponents();

    fixture = TestBed.createComponent(ScrapeSourceDetailComponent);
    component = fixture.componentInstance;
    fixture.detectChanges();
  });

  it('should create', () => {
    expect(component).toBeTruthy();
  });

  it('reloads the first page with server sort parameters when sorting proxies', () => {
    httpServiceStub.getScrapeSourceProxyPage.calls.reset();
    component.proxyPage.set(3);
    component.proxyPageSize.set(20);

    component.onProxySort({field: 'reputation', order: -1});

    expect(component.proxyPage()).toBe(1);
    expect(component.proxySortField()).toBe('reputation');
    expect(component.proxySortOrder()).toBe(-1);
    expect(httpServiceStub.getScrapeSourceProxyPage).toHaveBeenCalledWith(1, jasmine.objectContaining({
      page: 1,
      rows: 20,
      sortField: 'reputation',
      sortOrder: -1,
    }));
  });

  it('clears proxy sort on the third click of the same column', () => {
    component.proxySortField.set('reputation');
    component.proxySortOrder.set(-1);
    httpServiceStub.getScrapeSourceProxyPage.calls.reset();

    component.onProxyLazyLoad({first: 0, rows: 20, sortField: 'reputation', sortOrder: 1});
    component.onProxySort({field: 'reputation', order: 1});

    expect(component.proxySortField()).toBeNull();
    expect(component.proxySortOrder()).toBeNull();
    expect(httpServiceStub.getScrapeSourceProxyPage).toHaveBeenCalledWith(1, jasmine.objectContaining({
      sortField: null,
      sortOrder: null,
    }));
  });
});
