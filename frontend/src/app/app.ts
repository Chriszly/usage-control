import { Component, computed, inject } from '@angular/core';

import { Dashboard } from './dashboard/dashboard';
import { DevicePicker } from './devices/device-picker';
import { HistoryCharts } from './history/history';
import { I18n } from './i18n/i18n';
import { LanguageSwitcher } from './language/language-switcher';
import { ThemeService } from './theme/theme';

@Component({
  selector: 'app-root',
  imports: [Dashboard, DevicePicker, HistoryCharts, LanguageSwitcher],
  templateUrl: './app.html',
  styleUrl: './app.css',
})
export class App {
  protected readonly theme = inject(ThemeService);

  private readonly i18n = inject(I18n);

  protected readonly themeToggleLabel = computed(() =>
    this.i18n.t(this.theme.scheme() === 'dark' ? 'app.switchToLight' : 'app.switchToDark'),
  );
}
