import { provideHttpClient } from '@angular/common/http';
import { provideHttpClientTesting } from '@angular/common/http/testing';
import { TestBed } from '@angular/core/testing';

import { App } from './app';

describe('App', () => {
  afterEach(() => {
    document.documentElement.style.colorScheme = '';
  });

  it('shows the page title', () => {
    TestBed.configureTestingModule({
      imports: [App],
      providers: [provideHttpClient(), provideHttpClientTesting()],
    });
    const fixture = TestBed.createComponent(App);
    fixture.detectChanges();

    const heading = (fixture.nativeElement as HTMLElement).querySelector('h1');
    expect(heading?.textContent).toContain('Usage Control');
  });

  it('shows the mascot next to the page title', () => {
    TestBed.configureTestingModule({
      imports: [App],
      providers: [provideHttpClient(), provideHttpClientTesting()],
    });
    const fixture = TestBed.createComponent(App);
    fixture.detectChanges();

    const mascot = (fixture.nativeElement as HTMLElement).querySelector('header button img');
    expect(mascot?.getAttribute('src')).toBe('mascot.svg');
  });

  it('switches the theme when the mascot is clicked', () => {
    TestBed.configureTestingModule({
      imports: [App],
      providers: [provideHttpClient(), provideHttpClientTesting()],
    });
    const fixture = TestBed.createComponent(App);
    fixture.detectChanges();
    const button = (fixture.nativeElement as HTMLElement).querySelector('header button');

    (button as HTMLButtonElement).click();
    fixture.detectChanges();

    expect(document.documentElement.style.colorScheme).toBe('dark');
    expect(button?.getAttribute('aria-label')).toBe('Switch to light mode');
  });
});
