import { LOCALE_ID } from '@angular/core';
import { TestBed } from '@angular/core/testing';

import { LANGUAGE_COOKIE, LanguageSwitcher, RELOAD_PAGE } from './language-switcher';

describe('LanguageSwitcher', () => {
  let reload: ReturnType<typeof vi.fn>;

  function buttons(locale: string): HTMLButtonElement[] {
    reload = vi.fn();
    TestBed.configureTestingModule({
      imports: [LanguageSwitcher],
      providers: [
        { provide: LOCALE_ID, useValue: locale },
        { provide: RELOAD_PAGE, useValue: reload },
      ],
    });
    const fixture = TestBed.createComponent(LanguageSwitcher);
    fixture.detectChanges();
    return Array.from((fixture.nativeElement as HTMLElement).querySelectorAll('button'));
  }

  afterEach(() => {
    document.cookie = `${LANGUAGE_COOKIE}=; path=/; max-age=0`;
  });

  it('shows every language as a flag, named in that language', () => {
    const shown = buttons('en').map((b) => [
      b.getAttribute('aria-label'),
      b.querySelector('img')?.getAttribute('src'),
    ]);

    expect(shown).toEqual([
      ['English', 'flags/en.svg'],
      ['Deutsch', 'flags/de.svg'],
      ['Français', 'flags/fr.svg'],
      ['Español', 'flags/es.svg'],
    ]);
  });

  it('marks the language the page is shown in', () => {
    const current = buttons('fr').filter((b) => b.getAttribute('aria-pressed') === 'true');

    expect(current.map((b) => b.getAttribute('aria-label'))).toEqual(['Français']);
  });

  it('remembers the picked language and reloads the page at the same address', () => {
    buttons('en')[1].click();

    expect(document.cookie).toContain(`${LANGUAGE_COOKIE}=de`);
    expect(reload).toHaveBeenCalledOnce();
  });

  it('does nothing for the language already shown', () => {
    buttons('de')[1].click();

    expect(reload).not.toHaveBeenCalled();
  });
});
