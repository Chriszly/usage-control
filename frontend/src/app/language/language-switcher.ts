import { Component, LOCALE_ID, inject } from '@angular/core';

/** A language the website is translated into, with its name in that language. */
export interface Language {
  code: string;
  name: string;
}

/** Every language the website is built in; each one is served under /<code>/. */
export const LANGUAGES: readonly Language[] = [
  { code: 'en', name: 'English' },
  { code: 'de', name: 'Deutsch' },
  { code: 'fr', name: 'Français' },
  { code: 'es', name: 'Español' },
];

/** The cookie that tells the backend which language to open the next time. */
export const LANGUAGE_COOKIE = 'lang';

const ONE_YEAR_IN_SECONDS = 365 * 24 * 60 * 60;

/**
 * Links to the website in the other languages. The backend opens the page in
 * the language of the browser; picking one here remembers it instead.
 */
@Component({
  selector: 'app-language-switcher',
  templateUrl: './language-switcher.html',
  styleUrl: './language-switcher.css',
})
export class LanguageSwitcher {
  protected readonly languages = LANGUAGES;
  protected readonly current = inject(LOCALE_ID);

  protected remember(language: Language): void {
    document.cookie = `${LANGUAGE_COOKIE}=${language.code}; path=/; max-age=${ONE_YEAR_IN_SECONDS}; SameSite=Lax`;
  }
}
