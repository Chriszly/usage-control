import { DOCUMENT } from '@angular/common';
import { Component, InjectionToken, LOCALE_ID, inject } from '@angular/core';

/**
 * A language the website is translated into, with its name in that language.
 * Its flag is public/flags/<code>.svg.
 */
export interface Language {
  code: string;
  name: string;
}

/**
 * Every language the website is built in. The backend serves the one in the
 * language cookie, else the browser's language, at the same address.
 */
export const LANGUAGES: readonly Language[] = [
  { code: 'en', name: 'English' },
  { code: 'de', name: 'Deutsch' },
  { code: 'fr', name: 'Français' },
  { code: 'es', name: 'Español' },
];

/** The cookie that tells the backend which language to serve. */
export const LANGUAGE_COOKIE = 'lang';

const ONE_YEAR_IN_SECONDS = 365 * 24 * 60 * 60;

/** Reloads the page; a token so tests can replace it. */
export const RELOAD_PAGE = new InjectionToken<() => void>('reload the page', {
  factory: () => {
    const document = inject(DOCUMENT);
    return () => document.location.reload();
  },
});

/**
 * Flag buttons that switch the website to another language. The page starts in
 * the browser's language; picking one here remembers it and reloads the page,
 * which keeps its address.
 */
@Component({
  selector: 'app-language-switcher',
  templateUrl: './language-switcher.html',
  styleUrl: './language-switcher.css',
})
export class LanguageSwitcher {
  protected readonly languages = LANGUAGES;
  protected readonly current = inject(LOCALE_ID);
  private readonly document = inject(DOCUMENT);
  private readonly reload = inject(RELOAD_PAGE);

  protected switchTo(language: Language): void {
    if (language.code === this.current) {
      return;
    }
    this.document.cookie = `${LANGUAGE_COOKIE}=${language.code}; path=/; max-age=${ONE_YEAR_IN_SECONDS}; SameSite=Lax`;
    this.reload();
  }
}
