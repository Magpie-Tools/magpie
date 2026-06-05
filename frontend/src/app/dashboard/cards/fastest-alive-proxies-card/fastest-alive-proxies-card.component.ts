import {DecimalPipe, NgStyle} from '@angular/common';
import {Component, Input} from '@angular/core';
import {PrimeTemplate} from 'primeng/api';
import {Card} from 'primeng/card';
import {UIChart} from 'primeng/chart';

export interface FastestAliveProxyCountryLegend {
  country: string;
  color: string;
  count: number;
}

@Component({
  selector: 'app-fastest-alive-proxies-card',
  standalone: true,
  imports: [Card, PrimeTemplate, UIChart, DecimalPipe, NgStyle],
  templateUrl: './fastest-alive-proxies-card.component.html',
  styleUrl: './fastest-alive-proxies-card.component.scss'
})
export class FastestAliveProxiesCardComponent {
  @Input({ required: true }) chartData!: any;
  @Input({ required: true }) chartOptions!: any;
  @Input() proxyCount = 0;
  @Input() countryLegend: FastestAliveProxyCountryLegend[] = [];

  readonly cardStyleClass = 'chart-card bg-neutral-900 border border-neutral-800 fastest-alive-card';
  readonly reputationLegend = [
    { label: 'Good', className: 'shape-dot shape-dot--good' },
    { label: 'Neutral', className: 'shape-dot shape-dot--neutral' },
    { label: 'Bad', className: 'shape-dot shape-dot--bad' },
    { label: 'Unknown', className: 'shape-dot shape-dot--unknown' }
  ];

  get titleSuffix(): string {
    if (this.proxyCount <= 0) {
      return 'No alive proxies';
    }

    return `${this.proxyCount} fastest alive`;
  }
}
