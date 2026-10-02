import { DOCUMENT, registerLocaleData } from '@angular/common';
import localeDe from '@angular/common/locales/de';
import localeEs from '@angular/common/locales/es';
import localeFr from '@angular/common/locales/fr';
import { Injectable, InjectionToken, effect, inject, signal } from '@angular/core';

import { de } from './messages/de';
import { MessageKey, Messages, en } from './messages/en';
import { es } from './messages/es';
import { fr } from './messages/fr';

/** A language the website is translated into, with its name in that language. */
export interface Language {
  code: LanguageCode;
  name: string;
}

export type LanguageCode = 'en' | 'de' | 'fr' | 'es';

/** Every language the website is in. Its flag is public/flags/<code>.svg. */
export const LANGUAGES: readonly Language[] = [
  { code: 'en', name: 'English' },
  { code: 'de', name: 'Deutsch' },
  { code: 'fr', name: 'Français' },
  { code: 'es', name: 'Español' },
];

const MESSAGES: Record<LanguageCode, Messages> = { en, de, fr, es };

// Number and date formats for Angular's number and date pipes; English is built in.
registerLocaleData(localeDe);
registerLocaleData(localeFr);
registerLocaleData(localeEs);

/** Where the picked language is kept between visits, in the browser's localStorage. */
export const LANGUAGE_STORAGE_KEY = 'language';

/** The languages the browser asks for, most preferred first; a token so tests can replace it. */
export const BROWSER_LANGUAGES = new InjectionToken<readonly string[]>('browser languages', {
  factory: () => inject(DOCUMENT).defaultView?.navigator.languages ?? [],
});

/**
 * The language the page is shown in and its text. Everything on the page reads
 * the language through signals, so switching it updates the page in place.
 *
 * The page starts in the language picked last time, else the browser's
 * preferred language, else English.
 */
@Injectable({ providedIn: 'root' })
export class I18n {
  private readonly document = inject(DOCUMENT);

  readonly language = signal<LanguageCode>(
    this.storedLanguage() ?? preferredLanguage(inject(BROWSER_LANGUAGES)),
  );

  constructor() {
    effect(() => this.document.documentElement.setAttribute('lang', this.language()));
  }

  /** Shows the page in another language and remembers it for the next visit. */
  use(code: LanguageCode): void {
    this.language.set(code);
    try {
      this.document.defaultView?.localStorage.setItem(LANGUAGE_STORAGE_KEY, code);
    } catch {
      // Without storage (a private window, blocked site data) the choice lasts for this visit.
    }
  }

  /** The text for key in the page's language, with each {name} in it replaced by params[name]. */
  t(key: MessageKey, params: TextParams = {}): string {
    return translate(this.language(), key, params);
  }

  /** The singular or plural text for count, as the language's plural rules pick it. */
  plural(count: number, one: MessageKey, other: MessageKey): string {
    const form = new Intl.PluralRules(this.language()).select(count);
    return this.t(form === 'one' ? one : other, { count });
  }

  private storedLanguage(): LanguageCode | null {
    try {
      return asLanguage(this.document.defaultView?.localStorage.getItem(LANGUAGE_STORAGE_KEY));
    } catch {
      return null;
    }
  }
}

/** Values for the {name} placeholders in a text. */
export type TextParams = Record<string, string | number>;

/** The text for key in language, with each {name} in it replaced by params[name]. */
export function translate(
  language: LanguageCode,
  key: MessageKey,
  params: TextParams = {},
): string {
  return MESSAGES[language][key].replace(/\{(\w+)\}/g, (match, name: string) =>
    name in params ? String(params[name]) : match,
  );
}

/** The first of the browser's languages the website is in, such as "de" for "de-CH". */
export function preferredLanguage(browserLanguages: readonly string[]): LanguageCode {
  for (const tag of browserLanguages) {
    const language = asLanguage(tag.split('-')[0].toLowerCase());
    if (language) {
      return language;
    }
  }
  return 'en';
}

function asLanguage(value: string | null | undefined): LanguageCode | null {
  return LANGUAGES.find((language) => language.code === value)?.code ?? null;
}
