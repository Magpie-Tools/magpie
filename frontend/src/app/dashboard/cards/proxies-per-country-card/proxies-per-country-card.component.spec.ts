import { ComponentFixture, TestBed } from '@angular/core/testing';

import { ProxiesPerCountryCardComponent } from './proxies-per-country-card.component';

describe('ProxiesPerCountryCardComponent', () => {
  let component: ProxiesPerCountryCardComponent;
  let fixture: ComponentFixture<ProxiesPerCountryCardComponent>;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [ProxiesPerCountryCardComponent]
    })
    .compileComponents();

    fixture = TestBed.createComponent(ProxiesPerCountryCardComponent);
    component = fixture.componentInstance;
    fixture.detectChanges();
  });

  it('should create', () => {
    expect(component).toBeTruthy();
  });

  it('should resolve the Saint Lucia flag from common labels', () => {
    expect(component.countryFlag({name: 'Saint Lucia', percentage: 10})).toBe('🇱🇨');
    expect(component.countryFlag({name: 'St. Lucia', percentage: 10})).toBe('🇱🇨');
  });

  it('should resolve the DRC flag from the parenthesized Congo label', () => {
    expect(component.countryFlag({name: 'Congo (DRC)', percentage: 10})).toBe('🇨🇩');
  });
});
