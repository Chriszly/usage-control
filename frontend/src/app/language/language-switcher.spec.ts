import { TestBed } from '@angular/core/testing';

import { BROWSER_LANGUAGES, I18n, LANGUAGE_STORAGE_KEY } from '../i18n/i18n';
import { LanguageSwitcher } from './language-switcher';

describe('LanguageSwitcher', () => {
  function buttons(browserLanguage: string): HTMLButtonElement[] {
    TestBed.configureTestingModule({
      imports: [LanguageSwitcher],
      providers: [{ provide: BROWSER_LANGUAGES, useValue: [browserLanguage] }],
    });
    const fixture = TestBed.createComponent(LanguageSwitcher);
    fixture.detectChanges();
    return Array.from((fixture.nativeElement as HTMLElement).querySelectorAll('button'));
  }

  function pressed(all: HTMLButtonElement[]): (string | null)[] {
    return all
      .filter((b) => b.getAttribute('aria-pressed') === 'true')
      .map((b) => b.getAttribute('aria-label'));
  }

  afterEach(() => localStorage.removeItem(LANGUAGE_STORAGE_KEY));

  it('shows every language as a flag, named in that language', () => {
    const shown = buttons('en-GB').map((b) => [
      b.getAttribute('aria-label'),
      b.querySelector('img')?.getAttribute('src'),
    ]);

    expect(shown).toEqual([
      ['English (UK)', 'flags/en-GB.svg'],
      ['English (US)', 'flags/en-US.svg'],
      ['Deutsch', 'flags/de.svg'],
      ['Français', 'flags/fr.svg'],
      ['Español', 'flags/es.svg'],
    ]);
  });

  it('marks the language the page is shown in', () => {
    expect(pressed(buttons('fr-FR'))).toEqual(['Français']);
  });

  it('switches the language in place when a flag is clicked', () => {
    const all = buttons('en-GB');

    all[2].click();
    TestBed.tick();

    expect(TestBed.inject(I18n).language()).toBe('de');
    expect(pressed(all)).toEqual(['Deutsch']);
  });
});
