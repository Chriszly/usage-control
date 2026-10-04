import { BytesPipe } from './bytes.pipe';

describe('BytesPipe', () => {
  const pipe = new BytesPipe();

  it('shows small values in bytes', () => {
    expect(pipe.transform(0, 'en-GB')).toBe('0 B');
    expect(pipe.transform(1023, 'en-GB')).toBe('1023 B');
  });

  it('switches to the largest fitting unit', () => {
    expect(pipe.transform(1536, 'en-GB')).toBe('1.5 KiB');
    expect(pipe.transform(8 * 1024 ** 3, 'en-GB')).toBe('8.0 GiB');
  });

  it('stays at the largest unit for huge values', () => {
    expect(pipe.transform(2048 * 1024 ** 4, 'en-GB')).toBe('2048.0 TiB');
  });

  it("uses the language's decimal separator and units", () => {
    expect(pipe.transform(1536, 'de')).toBe('1,5 KiB');
    expect(pipe.transform(1536, 'fr')).toBe('1,5 Kio');
  });
});
