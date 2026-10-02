import { ComponentFixture, TestBed } from '@angular/core/testing';
import { MAT_DIALOG_DATA, MatDialogRef } from '@angular/material/dialog';

import { PasswordDialog, PasswordDialogData } from './password-dialog';

describe('PasswordDialog', () => {
  let fixture: ComponentFixture<PasswordDialog>;
  let close: ReturnType<typeof vi.fn>;

  async function open(data: PasswordDialogData): Promise<void> {
    close = vi.fn();
    TestBed.configureTestingModule({
      imports: [PasswordDialog],
      providers: [
        { provide: MAT_DIALOG_DATA, useValue: data },
        { provide: MatDialogRef, useValue: { close } },
      ],
    });
    fixture = TestBed.createComponent(PasswordDialog);
    fixture.detectChanges();
    await fixture.whenStable();
  }

  function element(): HTMLElement {
    return fixture.nativeElement as HTMLElement;
  }

  async function type(name: string, value: string): Promise<void> {
    const input = element().querySelector<HTMLInputElement>(`input[name="${name}"]`);
    if (!input) {
      throw new Error(`no input ${name}`);
    }
    input.value = value;
    input.dispatchEvent(new Event('input'));
    fixture.detectChanges();
    await fixture.whenStable();
    fixture.detectChanges();
  }

  function confirmButton(): HTMLButtonElement {
    return element().querySelector<HTMLButtonElement>('button[type="submit"]')!;
  }

  it('asks to choose a password of at least 8 characters, twice', async () => {
    await open({ action: 'add', deviceName: 'Office PC', passwordSet: false });

    expect(element().textContent).toContain('Choose a password');
    await type('password', 'short');
    await type('repeated', 'short');
    expect(confirmButton().disabled).toBe(true);

    await type('password', 'correct horse');
    await type('repeated', 'correct hors');
    expect(element().textContent).toContain('The passwords are not the same');
    expect(confirmButton().disabled).toBe(true);

    await type('repeated', 'correct horse');
    confirmButton().click();
    expect(close).toHaveBeenCalledWith({ password: 'correct horse', keepHistory: false });
  });

  it('asks for the password and whether to keep the history when removing', async () => {
    await open({ action: 'remove', deviceName: 'Office PC', passwordSet: true });

    expect(element().textContent).toContain('Enter the password');
    expect(element().textContent).toContain('Remove Office PC from the devices?');
    expect(element().querySelector('input[name="repeated"]')).toBeNull();

    await type('password', 'x');
    element().querySelector<HTMLInputElement>('mat-checkbox input')!.click();
    fixture.detectChanges();
    await fixture.whenStable();
    fixture.detectChanges();
    confirmButton().click();

    expect(close).toHaveBeenCalledWith({ password: 'x', keepHistory: true });
  });
});
