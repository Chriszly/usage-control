import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { MatDialog, MatDialogRef } from '@angular/material/dialog';
import { of } from 'rxjs';

import { DevicesDialog } from './devices-dialog';
import { DeviceService } from './devices';
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
      { id: 'pi', name: 'Pi', address: '192.168.1.20:9393', removable: false },
      { id: 'office-pc', name: 'Office PC', address: '192.168.1.30:9393', removable: true },
    ]);
    fixture = TestBed.createComponent(DevicesDialog);
    fixture.detectChanges();
  });

  afterEach(() => http.verify());

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

  it('lists the other devices and which can be removed', () => {
    expect(element().textContent).toContain('Pi');
    expect(element().textContent).toContain('Set in .env');
    expect(element().textContent).toContain('192.168.1.30:9393');
    expect(element().querySelectorAll('li button')).toHaveLength(1);
  });

  it('adds a device with the password and reads the list again', async () => {
    await type('name', 'Laptop');
    await type('address', '192.168.1.40:9393');
    button('Add').click();

    const add = http.expectOne('/api/devices');
    expect(add.request.method).toBe('POST');
    expect(add.request.body).toEqual({
      name: 'Laptop',
      address: '192.168.1.40:9393',
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

  it('says when the password is wrong', () => {
    button('Remove').click();

    const remove = http.expectOne('/api/devices/office-pc');
    expect(remove.request.method).toBe('DELETE');
    remove.flush({ problem: 'wrongPassword' }, { status: 403, statusText: 'Forbidden' });
    fixture.detectChanges();

    expect(element().textContent).toContain('The password is wrong.');
  });

  it('changes nothing when the password dialog is cancelled', () => {
    answer = undefined;

    button('Remove').click();

    http.expectNone('/api/devices/office-pc');
  });
});
