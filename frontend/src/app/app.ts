import { Component, inject } from '@angular/core';

import { Dashboard } from './dashboard/dashboard';
import { HistoryCharts } from './history/history';
import { ThemeService } from './theme/theme';

@Component({
  selector: 'app-root',
  imports: [Dashboard, HistoryCharts],
  templateUrl: './app.html',
  styleUrl: './app.css',
})
export class App {
  protected readonly theme = inject(ThemeService);
}
