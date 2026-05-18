import { ComponentFixture, TestBed } from '@angular/core/testing';
import { of } from 'rxjs';

import { DashboardComponent } from './dashboard.component';
import { GraphqlService } from '../services/graphql.service';

describe('DashboardComponent', () => {
  let component: DashboardComponent;
  let fixture: ComponentFixture<DashboardComponent>;
  let graphqlService: jasmine.SpyObj<GraphqlService>;

  beforeEach(async () => {
    graphqlService = jasmine.createSpyObj<GraphqlService>('GraphqlService', ['fetchDashboardData']);
    graphqlService.fetchDashboardData.and.returnValue(of({
      viewer: {
        dashboard: {
          totalChecks: 0,
          totalScraped: 0,
          totalChecksWeek: 0,
          totalScrapedWeek: 0,
          reputationBreakdown: {good: 0, neutral: 0, poor: 0, unknown: 0},
          countryBreakdown: [],
          judgeValidProxies: []
        },
        proxyCount: 0,
        proxyLimit: null,
        recentProxyChecks: [],
        proxyHistory: [],
        proxySnapshots: {alive: [], scraped: []},
        scrapeSourceCount: 0
      }
    }));

    await TestBed.configureTestingModule({
      imports: [DashboardComponent],
      providers: [
        {provide: GraphqlService, useValue: graphqlService}
      ]
    })
    .compileComponents();

    fixture = TestBed.createComponent(DashboardComponent);
    component = fixture.componentInstance;
    fixture.detectChanges();
  });

  it('should create', () => {
    expect(component).toBeTruthy();
  });

  it('should aggregate duplicate unknown country buckets', () => {
    (component as any).updateCountryBreakdown([
      {country: 'Unknown', count: 10},
      {country: 'N/A', count: 5},
      {country: 'unk', count: 3},
      {country: '', count: 2},
      {country: 'Germany', count: 20}
    ]);

    expect(component.majorCountries()).toEqual([
      {name: 'Unknown', value: 20, percentage: '50.0'},
      {name: 'Germany', value: 20, percentage: '50.0'}
    ]);
  });
});
