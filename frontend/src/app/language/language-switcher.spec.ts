import { LOCALE_ID } from '@angular/core';
import { TestBed } from '@angular/core/testing';

import { LANGUAGE_COOKIE, LanguageSwitcher } from './language-switcher';

describe('LanguageSwitcher', () => {
  function links(locale: string): HTMLAnchorElement[] {
    TestBed.configureTestingModule({
      imports: [LanguageSwitcher],
      providers: [{ provide: LOCALE_ID, useValue: locale }],
    });
    const fixture = TestBed.createComponent(LanguageSwitcher);
    fixture.detectChanges();
    return Array.from((fixture.nativeElement as HTMLElement).querySelectorAll('a'));
  }

  afterEach(() => {
    document.cookie = `${LANGUAGE_COOKIE}=; path=/; max-age=0`;
  });

  it('links to every language with its flag, named in that language', () => {
    const shown = links('en').map((a) => [
      a.getAttribute('aria-label'),
      a.getAttribute('href'),
      a.querySelector('img')?.getAttribute('src'),
    ]);

    expect(shown).toEqual([
      ['English', '/en/', 'flags/en.svg'],
      ['Deutsch', '/de/', 'flags/de.svg'],
      ['Français', '/fr/', 'flags/fr.svg'],
      ['Español', '/es/', 'flags/es.svg'],
    ]);
  });

  it('marks the language the page is shown in', () => {
    const current = links('fr').filter((a) => a.getAttribute('aria-current') === 'page');

    expect(current.map((a) => a.getAttribute('aria-label'))).toEqual(['Français']);
  });

  it('remembers the picked language for the next visit', () => {
    const german = links('en')[1];
    german.addEventListener('click', (event) => event.preventDefault());
    german.click();

    expect(document.cookie).toContain(`${LANGUAGE_COOKIE}=de`);
  });
});
