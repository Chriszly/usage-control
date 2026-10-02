import { Component } from '@angular/core';

import { Dashboard } from './dashboard/dashboard';
import { HistoryCharts } from './history/history';

@Component({
  selector: 'app-root',
  imports: [Dashboard, HistoryCharts],
  templateUrl: './app.html',
  styleUrl: './app.css',
})
export class App {}
