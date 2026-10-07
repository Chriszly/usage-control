import { formatExtra, localized } from './extras';

describe('localized', () => {
  const texts = { de: 'Leistung', 'en-US': 'Power draw' };

  it("picks the device's translation for the language, else for its base language", () => {
    expect(localized('Power', texts, 'de')).toBe('Leistung');
    expect(localized('Power', texts, 'en-US')).toBe('Power draw');
    expect(localized('Power', { en: 'Power use' }, 'en-GB')).toBe('Power use');
  });

  it('falls back to the English text', () => {
    expect(localized('Power', texts, 'fr')).toBe('Power');
    expect(localized('Power', undefined, 'es')).toBe('Power');
  });
});

describe('formatExtra', () => {
  it('writes each unit after the value', () => {
    expect(formatExtra(12.34, 'percent', 'en-GB')).toBe('12.3 %');
    expect(formatExtra(48, 'celsius', 'en-GB')).toBe('48 °C');
    expect(formatExtra(1536, 'bytes', 'en-GB')).toBe('1.5 KiB');
    expect(formatExtra(1536, 'bytesPerSecond', 'en-GB')).toBe('1.5 KiB/s');
    expect(formatExtra(3.25, 'watts', 'en-GB')).toBe('3.3 W');
    expect(formatExtra(0.4, 'milliseconds', 'en-GB')).toBe('0.4 ms');
    expect(formatExtra(12, 'perSecond', 'en-GB')).toBe('12/s');
    expect(formatExtra(1234.567, 'number', 'en-GB')).toBe('1,234.57');
  });

  it("uses the language's decimal separator", () => {
    expect(formatExtra(12.34, 'percent', 'de')).toBe('12,3 %');
    expect(formatExtra(1536, 'bytes', 'fr')).toBe('1,5 Kio');
  });
});
