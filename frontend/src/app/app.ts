import { Component, computed, inject } from '@angular/core';

import { Dashboard } from './dashboard/dashboard';
import { LanguageSwitcher } from './language/language-switcher';
import { ThemeService } from './theme/theme';

@Component({
  selector: 'app-root',
  imports: [Dashboard, LanguageSwitcher],
  templateUrl: './app.html',
  styleUrl: './app.css',
})
export class App {
  protected readonly theme = inject(ThemeService);

  protected readonly themeToggleLabel = computed(() =>
    this.theme.scheme() === 'dark'
      ? $localize`:Label of the mascot button@@app.switchToLight:Switch to light mode`
      : $localize`:Label of the mascot button@@app.switchToDark:Switch to dark mode`,
  );
}
