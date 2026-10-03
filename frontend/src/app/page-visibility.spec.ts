import { TestBed } from '@angular/core/testing';

import { PageVisibility } from './page-visibility';

describe('PageVisibility', () => {
  let visibilityState: DocumentVisibilityState;

  beforeEach(() => {
    vi.useFakeTimers();
    visibilityState = 'visible';
    vi.spyOn(document, 'visibilityState', 'get').mockImplementation(() => visibilityState);
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  /** Subscribes to the ticks every second and returns the ticks received so far. */
  function ticksEverySecond(): number[] {
    const ticks: number[] = [];
    TestBed.inject(PageVisibility)
      .ticks(1000)
      .subscribe((tick) => ticks.push(tick));
    vi.advanceTimersByTime(0);
    return ticks;
  }

  function show(state: DocumentVisibilityState): void {
    visibilityState = state;
    document.dispatchEvent(new Event('visibilitychange'));
    vi.advanceTimersByTime(0);
  }

  it('ticks at once and then every interval while the page is shown', () => {
    const ticks = ticksEverySecond();

    expect(ticks).toEqual([0]);
    vi.advanceTimersByTime(2000);
    expect(ticks).toEqual([0, 1, 2]);
  });

  it('stops ticking while the page is hidden and ticks at once when it is shown again', () => {
    const ticks = ticksEverySecond();

    show('hidden');
    vi.advanceTimersByTime(5000);
    expect(ticks).toEqual([0]);

    show('visible');
    expect(ticks).toEqual([0, 0]);
    vi.advanceTimersByTime(1000);
    expect(ticks).toEqual([0, 0, 1]);
  });

  it('ticks on when a visibility event leaves the page shown', () => {
    const ticks = ticksEverySecond();

    vi.advanceTimersByTime(1500);
    show('visible');
    vi.advanceTimersByTime(500);

    expect(ticks).toEqual([0, 1, 2]);
  });
});
