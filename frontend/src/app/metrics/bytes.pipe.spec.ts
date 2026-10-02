import { LOCALE_ID } from '@angular/core';
import { TestBed } from '@angular/core/testing';

import { BytesPipe } from './bytes.pipe';

function pipeFor(locale: string): BytesPipe {
  TestBed.configureTestingModule({ providers: [{ provide: LOCALE_ID, useValue: locale }] });
  return TestBed.runInInjectionContext(() => new BytesPipe());
}

describe('BytesPipe', () => {
  it('shows small values in bytes', () => {
    const pipe = pipeFor('en');
    expect(pipe.transform(0)).toBe('0 B');
    expect(pipe.transform(1023)).toBe('1023 B');
  });

  it('switches to the largest fitting unit', () => {
    const pipe = pipeFor('en');
    expect(pipe.transform(1536)).toBe('1.5 KiB');
    expect(pipe.transform(8 * 1024 ** 3)).toBe('8.0 GiB');
  });

  it('stays at the largest unit for huge values', () => {
    expect(pipeFor('en').transform(2048 * 1024 ** 4)).toBe('2048.0 TiB');
  });

  it("uses the page language's decimal separator", () => {
    expect(pipeFor('de').transform(1536)).toBe('1,5 KiB');
  });
});
