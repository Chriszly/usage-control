import { Component, inject } from '@angular/core';

import { Dashboard } from './dashboard/dashboard';
import { ThemeService } from './theme/theme';

@Component({
  selector: 'app-root',
  imports: [Dashboard],
  templateUrl: './app.html',
  styleUrl: './app.css',
})
export class App {
  protected readonly theme = inject(ThemeService);
}
