import { TestBed } from '@angular/core/testing';

import { BROWSER_LANGUAGES, I18n, LANGUAGE_STORAGE_KEY, preferredLanguage } from './i18n';

describe('I18n', () => {
  function i18n(browserLanguages: string[] = ['en-US']): I18n {
    TestBed.configureTestingModule({
      providers: [{ provide: BROWSER_LANGUAGES, useValue: browserLanguages }],
    });
    return TestBed.inject(I18n);
  }

  afterEach(() => localStorage.removeItem(LANGUAGE_STORAGE_KEY));

  it('starts in the language picked last time', () => {
    localStorage.setItem(LANGUAGE_STORAGE_KEY, 'es');

    expect(i18n(['de']).language()).toBe('es');
  });

  it("starts in the browser's language when none was picked", () => {
    expect(i18n(['it-IT', 'fr-CA', 'de']).language()).toBe('fr');
  });

  it('fills placeholders and follows a switch of language', () => {
    const service = i18n();
    expect(service.t('dashboard.usedOfTotal', { used: '1 GiB', total: '2 GiB' })).toBe(
      '1 GiB of 2 GiB',
    );

    service.use('de');

    expect(service.t('dashboard.usedOfTotal', { used: '1 GiB', total: '2 GiB' })).toBe(
      '1 GiB von 2 GiB',
    );
    expect(localStorage.getItem(LANGUAGE_STORAGE_KEY)).toBe('de');
  });

  it("picks the singular or plural by the language's rules", () => {
    const service = i18n();

    expect(service.plural(1, 'dashboard.cores.one', 'dashboard.cores.other')).toBe('1 core');
    expect(service.plural(4, 'dashboard.cores.one', 'dashboard.cores.other')).toBe('4 cores');
  });

  it('marks the page with its language', () => {
    i18n(['de']).use('fr');
    TestBed.tick();

    expect(document.documentElement.lang).toBe('fr');
  });
});

describe('preferredLanguage', () => {
  it('falls back to English', () => {
    expect(preferredLanguage(['it', 'ja'])).toBe('en');
    expect(preferredLanguage([])).toBe('en');
  });
});
