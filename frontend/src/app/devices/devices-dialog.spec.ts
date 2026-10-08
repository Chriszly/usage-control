import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { MatDialog, MatDialogRef } from '@angular/material/dialog';
import { of } from 'rxjs';

import { DevicesDialog } from './devices-dialog';
import { DeviceService, Suggestion } from './devices';
import { PasswordDialogResult } from './password-dialog';

describe('DevicesDialog', () => {
  let fixture: ComponentFixture<DevicesDialog>;
  let http: HttpTestingController;
  let answer: PasswordDialogResult | undefined;

  beforeEach(() => {
    answer = { password: 'correct horse', keepHistory: false };
    TestBed.configureTestingModule({
      imports: [DevicesDialog],
      providers: [provideHttpClient(), provideHttpClientTesting()],
    });
    http = TestBed.inject(HttpTestingController);
    vi.spyOn(TestBed.inject(MatDialog), 'open').mockImplementation(
      () => ({ afterClosed: () => of(answer) }) as unknown as MatDialogRef<unknown>,
    );
    TestBed.inject(DeviceService).devices.set([
      { id: 'local', name: '' },
      { id: 'pi', name: 'Pi', address: '192.168.1.20:9393', kind: 'server', removable: false },
      {
        id: 'office-pc',
        name: 'Office PC',
        address: '192.168.1.30:9393',
        kind: 'server',
        removable: true,
      },
    ]);
    fixture = TestBed.createComponent(DevicesDialog);
    fixture.detectChanges();
  });

  /** Answers the dialog's question which device the page is open on. */
  async function suggest(suggestion: Suggestion | null): Promise<void> {
    http.expectOne('/api/devices/suggestion').flush(suggestion);
    fixture.detectChanges();
    await fixture.whenStable();
    fixture.detectChanges();
  }

  function input(name: string): HTMLInputElement {
    return element().querySelector<HTMLInputElement>(`input[name="${name}"]`)!;
  }

  afterEach(() => {
    // Tests that do not look at the suggestion leave it unanswered.
    http.match('/api/devices/suggestion').forEach((r) => r.flush(null));
    http.verify();
  });

  function element(): HTMLElement {
    return fixture.nativeElement as HTMLElement;
  }

  async function type(name: string, value: string): Promise<void> {
    const input = element().querySelector<HTMLInputElement>(`input[name="${name}"]`)!;
    input.value = value;
    input.dispatchEvent(new Event('input'));
    fixture.detectChanges();
    await fixture.whenStable();
    fixture.detectChanges();
  }

  function button(label: string): HTMLButtonElement {
    const buttons = Array.from(element().querySelectorAll('button'));
    return buttons.find((b) => b.textContent?.trim() === label)!;
  }

  /** The button that picks a kind in the form to add a device. */
  function formKind(label: string): HTMLButtonElement {
    const buttons = Array.from(element().querySelectorAll('form .kind button'));
    return buttons.find((b) => b.textContent?.trim() === label) as HTMLButtonElement;
  }

  it('fills in the device the page is open on', async () => {
    await suggest({ address: '192.168.1.47:9393', name: 'Kitchen tablet', kind: 'pc' });

    expect(input('name').value).toBe('Kitchen tablet');
    expect(input('address').value).toBe('192.168.1.47:9393');
    expect(formKind('PC / laptop').getAttribute('aria-pressed')).toBe('true');
    expect(element().textContent).toContain('Filled in with the device this page is open on.');

    await type('address', '192.168.1.48:9393');
    expect(element().textContent).not.toContain('Filled in with the device');
  });

  it('keeps what was typed before the suggestion arrives', async () => {
    await type('name', 'Laptop');
    await suggest({ address: '192.168.1.47:9393', name: 'Kitchen tablet', kind: 'pc' });

    expect(input('name').value).toBe('Laptop');
    expect(input('address').value).toBe('');
  });

  it('leaves the form empty without a suggestion', async () => {
    await suggest(null);

    expect(input('name').value).toBe('');
    expect(input('address').value).toBe('');
    expect(element().textContent).not.toContain('Filled in with the device');
  });

  it('lists the other devices and which can be removed', () => {
    expect(element().textContent).toContain('Pi');
    expect(element().textContent).toContain('Set in .env');
    expect(element().textContent).toContain('192.168.1.30:9393');
    expect(element().querySelectorAll('li > button')).toHaveLength(1);
  });

  it('adds a device with the password and reads the list again', async () => {
    await type('name', 'Laptop');
    await type('address', '192.168.1.40:9393');
    formKind('PC / laptop').click();
    fixture.detectChanges();
    button('Add').click();

    const add = http.expectOne('/api/devices');
    expect(add.request.method).toBe('POST');
    expect(add.request.body).toEqual({
      name: 'Laptop',
      address: '192.168.1.40:9393',
      kind: 'pc',
      password: 'correct horse',
    });
    add.flush(
      { id: 'laptop', name: 'Laptop', removable: true },
      { status: 201, statusText: 'Created' },
    );
    http
      .expectOne((r) => r.method === 'GET' && r.url === '/api/devices')
      .flush({ devices: [{ id: 'local', name: '' }], passwordSet: true });
    fixture.detectChanges();
    await fixture.whenStable();

    expect(TestBed.inject(DeviceService).passwordSet()).toBe(true);
    expect((element().querySelector('input[name="name"]') as HTMLInputElement).value).toBe('');
  });

  it('counts a change as done when the list cannot be read afterwards', async () => {
    await type('name', 'Laptop');
    await type('address', '192.168.1.40:9393');
    button('Add').click();

    http
      .expectOne('/api/devices')
      .flush(
        { id: 'laptop', name: 'Laptop', removable: true },
        { status: 201, statusText: 'Created' },
      );
    http
      .expectOne((r) => r.method === 'GET' && r.url === '/api/devices')
      .flush('down', { status: 502, statusText: 'Bad Gateway' });
    fixture.detectChanges();
    await fixture.whenStable();

    expect(element().querySelector('.error')).toBeNull();
    expect((element().querySelector('input[name="name"]') as HTMLInputElement).value).toBe('');
    expect(button('Remove').disabled).toBe(false);
  });

  it('says when the password is wrong', () => {
    button('Remove').click();

    const remove = http.expectOne('/api/devices/office-pc');
    expect(remove.request.method).toBe('DELETE');
    remove.flush({ problem: 'wrongPassword' }, { status: 403, statusText: 'Forbidden' });
    fixture.detectChanges();

    expect(element().textContent).toContain('The password is wrong.');
    // A change that failed may still have been made, so the list is read again.
    http
      .expectOne((r) => r.method === 'GET' && r.url === '/api/devices')
      .flush({ devices: [{ id: 'local', name: '' }], passwordSet: true });
  });

  it('lets a failed change be tried again while the list is read again', () => {
    button('Remove').click();

    http
      .expectOne('/api/devices/office-pc')
      .flush('down', { status: 504, statusText: 'Gateway Timeout' });
    fixture.detectChanges();

    expect(button('Remove').disabled).toBe(false);
    http
      .expectOne((r) => r.method === 'GET' && r.url === '/api/devices')
      .flush({ devices: [{ id: 'local', name: '' }], passwordSet: true });
  });

  it('keeps a new change busy when an earlier failed change reads the list late', () => {
    button('Remove').click();
    http
      .expectOne('/api/devices/office-pc')
      .flush('down', { status: 504, statusText: 'Gateway Timeout' });
    fixture.detectChanges();
    const staleReload = http.expectOne((r) => r.method === 'GET' && r.url === '/api/devices');

    button('Remove').click();
    fixture.detectChanges();

    // The new change stops the failed change's reading of the list.
    expect(staleReload.cancelled).toBe(true);
    expect(button('Remove').disabled).toBe(true);
    http.expectOne('/api/devices/office-pc').flush(null, { status: 204, statusText: 'No Content' });
    http
      .expectOne((r) => r.method === 'GET' && r.url === '/api/devices')
      .flush({ devices: [{ id: 'local', name: '' }], passwordSet: true });
  });

  it('stops reading the list once the dialog is closed', async () => {
    await suggest(null);
    button('Remove').click();
    http
      .expectOne('/api/devices/office-pc')
      .flush('down', { status: 504, statusText: 'Gateway Timeout' });
    const reload = http.expectOne((r) => r.method === 'GET' && r.url === '/api/devices');

    fixture.destroy();

    expect(reload.cancelled).toBe(true);
  });

  it('finishes a change once the dialog is closed, without reading the list', async () => {
    await suggest(null);
    button('Remove').click();
    const remove = http.expectOne('/api/devices/office-pc');

    fixture.destroy();

    expect(remove.cancelled).toBe(false);
    remove.flush(null, { status: 204, statusText: 'No Content' });
    http.expectNone((r) => r.method === 'GET' && r.url === '/api/devices');
  });

  it("says when the address is the hub's own", async () => {
    await type('name', 'Laptop');
    await type('address', '127.0.0.1:9393');
    button('Add').click();

    http
      .expectOne('/api/devices')
      .flush({ problem: 'addressOwn' }, { status: 400, statusText: 'Bad Request' });
    fixture.detectChanges();

    expect(element().textContent).toContain('This address is the hub itself.');
    http
      .expectOne((r) => r.method === 'GET' && r.url === '/api/devices')
      .flush({ devices: [{ id: 'local', name: '' }], passwordSet: true });
  });

  it('changes nothing when the password dialog is cancelled', () => {
    answer = undefined;

    button('Remove').click();

    http.expectNone('/api/devices/office-pc');
  });

  it('changes the kind of a listed device with the password', () => {
    const pi = element().querySelectorAll('li')[0];
    const pc = Array.from(pi.querySelectorAll('button')).find(
      (b) => b.textContent?.trim() === 'PC / laptop',
    )!;
    expect(pc.getAttribute('aria-pressed')).toBe('false');

    pc.click();

    const change = http.expectOne('/api/devices/pi/kind');
    expect(change.request.method).toBe('PUT');
    expect(change.request.body).toEqual({ kind: 'pc', password: 'correct horse' });
    change.flush(null, { status: 204, statusText: 'No Content' });
    http
      .expectOne((r) => r.method === 'GET' && r.url === '/api/devices')
      .flush({ devices: [{ id: 'local', name: '' }], passwordSet: true });
  });

  it('asks nothing when the kind the device has is picked again', () => {
    const pi = element().querySelectorAll('li')[0];
    Array.from(pi.querySelectorAll('button'))
      .find((b) => b.textContent?.trim() === 'Server / IoT')!
      .click();

    http.expectNone('/api/devices/pi/kind');
  });
});
