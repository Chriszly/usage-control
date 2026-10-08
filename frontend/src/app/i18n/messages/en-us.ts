import { Messages, en } from './en';

/**
 * The website's text in American English: the British text, with only what is
 * written differently in the United States, such as the 12-hour clock.
 */
export const enUS: Messages = {
  ...en,
  'format.time': 'h:mm a',
  'format.timeSeconds': 'h:mm:ss a',
  'format.weekdayTime': 'EEE h:mm a',
  'format.dayMonth': 'MMM d',
  'format.dateTime': 'EEE, MMM d, h:mm a',
  'format.dateTimeSeconds': 'EEE, MMM d, h:mm:ss a',
  'format.dayMonthYear': 'MMM d, y',
  'format.dateTimeYear': 'EEE, MMM d, y, h:mm a',
  'format.dateTimeSecondsYear': 'EEE, MMM d, y, h:mm:ss a',
};
