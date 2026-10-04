import { TestBed } from '@angular/core/testing';

import { I18n } from '../app/i18n/i18n';
import { DEMO_BUILD, DemoBuild } from './demo-build';
import { DemoNotice } from './demo-notice';

describe('DemoNotice', () => {
  function render(build: DemoBuild): HTMLElement {
    TestBed.configureTestingModule({ providers: [{ provide: DEMO_BUILD, useValue: build }] });
    TestBed.inject(I18n).language.set('en-GB');
    const fixture = TestBed.createComponent(DemoNotice);
    fixture.detectChanges();
    return fixture.nativeElement as HTMLElement;
  }

  it('names the release and links to the demo of main', () => {
    const notice = render({ release: '1.2.0' });
    expect(notice.textContent).toContain('Version 1.2.0');
    const link = notice.querySelector('a');
    expect(link?.getAttribute('href')).toBe('main/');
    expect(link?.textContent?.trim()).toBe('See the development version');
  });

  it('names the commit of main and links back to the release', () => {
    const notice = render({ commit: 'a690bc7' });
    expect(notice.textContent).toContain('Development version from main a690bc7');
    const link = notice.querySelector('a');
    expect(link?.getAttribute('href')).toBe('../');
    expect(link?.textContent?.trim()).toBe('See the latest release');
  });

  it('has no link to another demo when it is neither published one, such as a local build', () => {
    const notice = render({});
    expect(notice.textContent).toContain('Development version from main');
    const links = Array.from(notice.querySelectorAll('a')).map((a) => a.getAttribute('href'));
    expect(links).toEqual(['https://github.com/Chriszly/usage-control#readme']);
  });
});
