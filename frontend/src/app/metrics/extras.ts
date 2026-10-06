import { formatNumber } from '@angular/common';

import { LanguageCode } from '../i18n/i18n';
import { BytesPipe } from './bytes.pipe';

/**
 * How a value of an extra is shown. A device running a newer version may
 * send a unit this page does not know; the hub turns it into "number".
 */
export type ExtraUnit =
  | 'percent'
  | 'celsius'
  | 'bytes'
  | 'bytesPerSecond'
  | 'watts'
  | 'milliseconds'
  | 'perSecond'
  | 'number'
  | 'text';

/**
 * A group of values a device reports beyond the fixed ones, described well
 * enough to show without knowing it: its title in English, and in other
 * languages by language code.
 */
export interface Extra {
  id: string;
  title: string;
  titles?: Record<string, string>;
  items: ExtraItem[];
}

/** One value of an extra. Text values have `text`, all others `value`. */
export interface ExtraItem {
  id: string;
  label: string;
  labels?: Record<string, string>;
  unit: ExtraUnit;
  value?: number;
  text?: string;
  /** Whether the hub keeps its history, which is then drawn as a chart. */
  history?: boolean;
}

/** Describes the stored values of an extra, so its chart can be drawn. */
export interface ExtraInfo {
  title: string;
  titles?: Record<string, string>;
  label: string;
  labels?: Record<string, string>;
  unit: ExtraUnit;
}

/**
 * The text in the page's language: the device's translation for it, else
 * the one for its base language ("de" for "de-AT"), else the English text.
 */
export function localized(
  text: string,
  texts: Record<string, string> | undefined,
  language: LanguageCode,
): string {
  return texts?.[language] ?? texts?.[language.split('-')[0]] ?? text;
}

const bytes = new BytesPipe();

/** A value of an extra with its unit, formatted for the page's language. */
export function formatExtra(value: number, unit: ExtraUnit, language: LanguageCode): string {
  const decimal = (digits: string) => formatNumber(value, language, digits);
  switch (unit) {
    case 'percent':
      return `${decimal('1.0-1')} %`;
    case 'celsius':
      return `${decimal('1.0-1')} °C`;
    case 'bytes':
      return bytes.transform(value, language);
    case 'bytesPerSecond':
      return `${bytes.transform(value, language)}/s`;
    case 'watts':
      return `${decimal('1.0-1')} W`;
    case 'milliseconds':
      return `${decimal('1.0-1')} ms`;
    case 'perSecond':
      return `${decimal('1.0-1')}/s`;
    default:
      return decimal('1.0-2');
  }
}
