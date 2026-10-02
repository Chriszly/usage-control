import { Pipe, PipeTransform } from '@angular/core';

const UNITS = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];

/** Formats a number of bytes for people, for example 1536 as "1.5 KiB". */
@Pipe({ name: 'bytes' })
export class BytesPipe implements PipeTransform {
  transform(bytes: number): string {
    let value = bytes;
    let unit = 0;
    while (value >= 1024 && unit < UNITS.length - 1) {
      value /= 1024;
      unit++;
    }
    const digits = unit === 0 ? 0 : 1;
    return `${value.toFixed(digits)} ${UNITS[unit]}`;
  }
}
