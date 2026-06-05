import { ComponentFixture, TestBed } from '@angular/core/testing';

import { FastestAliveProxiesCardComponent } from './fastest-alive-proxies-card.component';

describe('FastestAliveProxiesCardComponent', () => {
  let component: FastestAliveProxiesCardComponent;
  let fixture: ComponentFixture<FastestAliveProxiesCardComponent>;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [FastestAliveProxiesCardComponent]
    })
      .compileComponents();

    fixture = TestBed.createComponent(FastestAliveProxiesCardComponent);
    component = fixture.componentInstance;
    component.chartData = { datasets: [] };
    component.chartOptions = {};
    fixture.detectChanges();
  });

  it('should create', () => {
    expect(component).toBeTruthy();
  });
});
