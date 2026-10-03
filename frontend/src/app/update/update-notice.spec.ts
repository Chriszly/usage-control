import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { ComponentFixture, TestBed } from '@angular/core/testing';

import { UPDATE_REFRESH_MS, UpdateNotice } from './update-notice';

describe('UpdateNotice', () => {
  let fixture: ComponentFixture<UpdateNotice>;
  let http: HttpTestingController;

  beforeEach(() => {
    vi.useFakeTimers();
    TestBed.configureTestingModule({
      imports: [UpdateNotice],
      providers: [provideHttpClient(), provideHttpClientTesting()],
    });
    http = TestBed.inject(HttpTestingController);
    fixture = TestBed.createComponent(UpdateNotice);
    fixture.detectChanges();
    vi.advanceTimersByTime(0);
  });

  afterEach(() => {
    http.verify();
    vi.useRealTimers();
  });

  function element(): HTMLElement {
    return fixture.nativeElement as HTMLElement;
  }

  it('names a newer release with a link to what changed', () => {
    http.expectOne('/api/update').flush({
      current: '0.1.0',
      latest: '0.2.0',
      url: 'https://github.com/Chriszly/usage-control/releases/tag/v0.2.0',
    });
    fixture.detectChanges();

    expect(element().textContent).toContain('Version 0.2.0 is available (this device runs 0.1.0).');
    const link = element().querySelector('a');
    expect(link?.getAttribute('href')).toBe(
      'https://github.com/Chriszly/usage-control/releases/tag/v0.2.0',
    );
    expect(link?.textContent).toContain("What's new");
  });

  it('shows nothing when the device runs the newest release', () => {
    http.expectOne('/api/update').flush({ current: '0.2.0' });
    fixture.detectChanges();
    expect(element().querySelector('.notice')).toBeNull();
  });

  it('shows nothing when the backend cannot tell, and asks again later', () => {
    http.expectOne('/api/update').flush('', { status: 404, statusText: 'Not Found' });
    fixture.detectChanges();
    expect(element().querySelector('.notice')).toBeNull();

    vi.advanceTimersByTime(UPDATE_REFRESH_MS);
    http
      .expectOne('/api/update')
      .flush({ current: '0.1.0', latest: '0.2.0', url: 'https://github.com/x' });
    fixture.detectChanges();
    expect(element().querySelector('.notice')).not.toBeNull();
  });
});
