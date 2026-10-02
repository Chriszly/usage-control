import { LOCALE_ID, Pipe, PipeTransform, inject } from '@angular/core';

const UNITS = [
  $localize`:Abbreviation for bytes@@bytes.unit.b:B`,
  $localize`:Abbreviation for kibibytes@@bytes.unit.kib:KiB`,
  $localize`:Abbreviation for mebibytes@@bytes.unit.mib:MiB`,
  $localize`:Abbreviation for gibibytes@@bytes.unit.gib:GiB`,
  $localize`:Abbreviation for tebibytes@@bytes.unit.tib:TiB`,
];

/**
 * Formats a number of bytes for people in the page's language, for example
 * 1536 as "1.5 KiB" in English and "1,5 KiB" in German.
 */
@Pipe({ name: 'bytes' })
export class BytesPipe implements PipeTransform {
  private readonly locale = inject(LOCALE_ID);

  transform(bytes: number): string {
    let value = bytes;
    let unit = 0;
    while (value >= 1024 && unit < UNITS.length - 1) {
      value /= 1024;
      unit++;
    }
    const digits = unit === 0 ? 0 : 1;
    const number = new Intl.NumberFormat(this.locale, {
      minimumFractionDigits: digits,
      maximumFractionDigits: digits,
      useGrouping: false,
    }).format(value);
    return `${number} ${UNITS[unit]}`;
  }
}
