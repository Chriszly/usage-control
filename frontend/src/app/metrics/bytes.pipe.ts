import { Pipe, PipeTransform } from '@angular/core';

import { LanguageCode, translate } from '../i18n/i18n';
import { MessageKey } from '../i18n/messages/en';

const UNITS: MessageKey[] = [
  'bytes.unit.b',
  'bytes.unit.kib',
  'bytes.unit.mib',
  'bytes.unit.gib',
  'bytes.unit.tib',
];

/**
 * Formats a number of bytes for people in the given language, for example
 * 1536 as "1.5 KiB" in English and "1,5 Kio" in French. Pass the page's
 * language, `i18n.language()`, so the value updates when it switches.
 */
@Pipe({ name: 'bytes' })
export class BytesPipe implements PipeTransform {
  transform(bytes: number, language: LanguageCode): string {
    let value = bytes;
    let unit = 0;
    while (value >= 1024 && unit < UNITS.length - 1) {
      value /= 1024;
      unit++;
    }
    const digits = unit === 0 ? 0 : 1;
    const number = new Intl.NumberFormat(language, {
      minimumFractionDigits: digits,
      maximumFractionDigits: digits,
      useGrouping: false,
    }).format(value);
    return `${number} ${translate(language, UNITS[unit])}`;
  }
}
