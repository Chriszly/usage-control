import { formatDate } from '@angular/common';
import { TestBed } from '@angular/core/testing';

import { BROWSER_LANGUAGES, I18n, LANGUAGE_STORAGE_KEY, fillIn, preferredLanguage } from './i18n';
import { MessageKey } from './messages/en';

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

  it('starts in British English when English was picked before it was split', () => {
    localStorage.setItem(LANGUAGE_STORAGE_KEY, 'en');

    expect(i18n(['en-US']).language()).toBe('en-GB');
  });

  it('writes dates the American way in American English and the British way in British', () => {
    const service = i18n(['en-US']);
    const time = Date.UTC(2026, 9, 4, 17, 5);
    const format = (key: MessageKey) => formatDate(time, service.t(key), service.language(), 'UTC');

    expect(format('format.dateTime')).toBe('Sun, Oct 4, 5:05 PM');
    expect(formatDate(time, 'short', service.language(), 'UTC')).toBe('10/4/26, 5:05\u202fPM');

    service.use('en-GB');

    expect(format('format.dateTime')).toBe('Sun 4 Oct, 17:05');
    expect(formatDate(time, 'short', service.language(), 'UTC')).toBe('04/10/2026, 17:05');
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

describe('fillIn', () => {
  it('replaces each {name} that has a value and keeps the others', () => {
    expect(fillIn('Version {version} of {name}', { version: '1.1.3c' })).toBe(
      'Version 1.1.3c of {name}',
    );
    expect(fillIn('{count} devices', { count: 2 })).toBe('2 devices');
  });
});

describe('preferredLanguage', () => {
  it('falls back to British English', () => {
    expect(preferredLanguage(['it', 'ja'])).toBe('en-GB');
    expect(preferredLanguage([])).toBe('en-GB');
  });

  it('picks American English only for American browsers', () => {
    expect(preferredLanguage(['en-US', 'en'])).toBe('en-US');
    expect(preferredLanguage(['en'])).toBe('en-GB');
    expect(preferredLanguage(['en-AU'])).toBe('en-GB');
    expect(preferredLanguage(['en-GB'])).toBe('en-GB');
  });
});
