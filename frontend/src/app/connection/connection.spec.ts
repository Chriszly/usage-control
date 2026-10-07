import { HttpClient, provideHttpClient, withInterceptors } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { ComponentFixture, TestBed } from '@angular/core/testing';

import { DeviceService } from '../devices/devices';
import { ANSWER_TIMEOUT_MS, HubConnection, hubConnectionInterceptor } from './connection';
import { ConnectionBanner } from './connection-banner';

describe('HubConnection', () => {
  let http: HttpTestingController;
  let client: HttpClient;
  let connection: HubConnection;
  let fixture: ComponentFixture<ConnectionBanner>;

  beforeEach(() => {
    TestBed.configureTestingModule({
      imports: [ConnectionBanner],
      providers: [
        provideHttpClient(withInterceptors([hubConnectionInterceptor])),
        provideHttpClientTesting(),
      ],
    });
    http = TestBed.inject(HttpTestingController);
    client = TestBed.inject(HttpClient);
    connection = TestBed.inject(HubConnection);
    fixture = TestBed.createComponent(ConnectionBanner);
    fixture.detectChanges();
  });

  afterEach(() => http.verify());

  function banner(): HTMLElement | null {
    fixture.detectChanges();
    return (fixture.nativeElement as HTMLElement).querySelector('.banner');
  }

  function get(respond: (request: ReturnType<HttpTestingController['expectOne']>) => void): void {
    client.get('/api/metrics').subscribe({ error: () => undefined });
    respond(http.expectOne('/api/metrics'));
  }

  it('shows nothing while the hub answers', () => {
    get((r) => r.flush({}));
    expect(connection.lost()).toBe(false);
    expect(banner()).toBeNull();
  });

  it('says across the page that nothing is live when a request gets no answer', () => {
    get((r) => r.error(new ProgressEvent('error')));

    expect(connection.lost()).toBe(true);
    expect(banner()?.getAttribute('role')).toBe('alert');
    expect(banner()?.textContent).toContain('Host Hub is not answering');
    expect(banner()?.textContent).toContain('nothing on this page is live');
  });

  it('names the hub by its name when it has one', () => {
    TestBed.inject(DeviceService).devices.set([{ id: 'local', name: 'Garage Pi' }]);
    get((r) => r.error(new ProgressEvent('error')));

    expect(banner()?.textContent).toContain('Garage Pi is not answering');
  });

  it('counts a proxy that cannot reach the hub as no answer', () => {
    get((r) => r.flush('', { status: 502, statusText: 'Bad Gateway' }));
    expect(connection.lost()).toBe(true);
  });

  it('gives up on a request the hub never answers and says so', () => {
    vi.useFakeTimers();
    try {
      let failed: unknown = null;
      client.get('/api/metrics').subscribe({ error: (error: unknown) => (failed = error) });
      const request = http.expectOne('/api/metrics');

      vi.advanceTimersByTime(ANSWER_TIMEOUT_MS - 1);
      expect(connection.lost()).toBe(false);

      vi.advanceTimersByTime(1);
      expect(failed).not.toBeNull();
      expect(request.cancelled).toBe(true);
      expect(connection.lost()).toBe(true);
      expect(banner()).not.toBeNull();
    } finally {
      vi.useRealTimers();
    }
  });

  it('gives page files as long as they take', () => {
    vi.useFakeTimers();
    try {
      client.get('/main.js', { responseType: 'text' }).subscribe();
      const request = http.expectOne('/main.js');

      vi.advanceTimersByTime(ANSWER_TIMEOUT_MS * 2);
      expect(request.cancelled).toBe(false);
      expect(connection.lost()).toBe(false);
      request.flush('');
    } finally {
      vi.useRealTimers();
    }
  });

  it('gives changes to the devices as long as they take', () => {
    vi.useFakeTimers();
    try {
      client.post('/api/devices', {}).subscribe();
      const request = http.expectOne('/api/devices');

      vi.advanceTimersByTime(ANSWER_TIMEOUT_MS * 2);
      expect(request.cancelled).toBe(false);
      expect(connection.lost()).toBe(false);
      request.flush({});
    } finally {
      vi.useRealTimers();
    }
  });

  it('keeps the time the hub stopped answering', () => {
    get((r) => r.error(new ProgressEvent('error')));
    const since = connection.lostSince();
    get((r) => r.error(new ProgressEvent('error')));
    expect(connection.lostSince()).toBe(since);
  });

  it('takes the banner away as soon as the hub answers, an error it sends too', () => {
    get((r) => r.error(new ProgressEvent('error')));
    expect(banner()).not.toBeNull();

    // A device the hub collects from that does not answer is the hub answering.
    get((r) => r.flush('', { status: 503, statusText: 'Service Unavailable' }));
    expect(connection.lost()).toBe(false);
    expect(banner()).toBeNull();
  });
});
