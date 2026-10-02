import { Component, inject } from '@angular/core';

import { I18n, LANGUAGES } from '../i18n/i18n';

/** Flag buttons that switch the page to another language in place. */
@Component({
  selector: 'app-language-switcher',
  templateUrl: './language-switcher.html',
  styleUrl: './language-switcher.css',
})
export class LanguageSwitcher {
  protected readonly i18n = inject(I18n);
  protected readonly languages = LANGUAGES;
}
