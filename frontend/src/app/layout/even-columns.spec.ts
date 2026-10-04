import { Component, signal } from '@angular/core';
import { TestBed } from '@angular/core/testing';

import { EvenColumns, evenColumns, minCardWidth } from './even-columns';

/** Stands in for the browser's ResizeObserver, which jsdom lacks; tests report sizes by hand. */
class FakeResizeObserver {
  static last: FakeResizeObserver | undefined;
  constructor(readonly callback: (entries: { contentRect: { width: number } }[]) => void) {
    FakeResizeObserver.last = this;
  }
  observe = vi.fn();
  disconnect = vi.fn();
  resize(width: number): void {
    this.callback([{ contentRect: { width } }]);
  }
}

describe('evenColumns', () => {
  // Cards at least 224 pixels wide with 16 pixels between them: a 1000 pixel grid fits 4.
  const columns = (cards: number, width: number) => evenColumns(cards, width, 224, 16);

  it('keeps every card on one row when they all fit', () => {
    expect(columns(3, 1000)).toBe(3);
    expect(columns(4, 1000)).toBe(4);
  });

  it('evens out the rows when the cards wrap', () => {
    expect(columns(5, 1000)).toBe(3); // 3 + 2, not 4 + 1
    expect(columns(7, 1456)).toBe(4); // fits 6: 4 + 3, not 6 + 1
    expect(columns(8, 1696)).toBe(4); // fits 7: 4 + 4, not 7 + 1
    expect(columns(10, 1000)).toBe(4); // 4 + 4 + 2, the fewest rows
  });

  it('gives a grid narrower than one card a single column', () => {
    expect(columns(9, 200)).toBe(1);
    expect(columns(0, 1000)).toBe(1);
  });
});

describe('minCardWidth', () => {
  it('turns the rem of --min-card into pixels', () => {
    expect(minCardWidth('14rem', 16)).toBe(224);
    expect(minCardWidth(' 12.5rem', 20)).toBe(250);
  });

  it('refuses a width that is missing or not in rem', () => {
    expect(() => minCardWidth('', 16)).toThrowError(/--min-card/);
    expect(() => minCardWidth('224px', 16)).toThrowError(/--min-card/);
  });
});

@Component({
  imports: [EvenColumns],
  template: `
    <div appEvenColumns style="--min-card: 14rem">
      @for (card of cards(); track card) {
        <div>{{ card }}</div>
      }
    </div>
  `,
})
class Host {
  readonly cards = signal(['CPU', 'Memory', 'Disks', 'Network', 'Uptime']);
}

describe('EvenColumns', () => {
  beforeEach(() => {
    vi.stubGlobal('ResizeObserver', FakeResizeObserver);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  /** jsdom computes no gap, so it is 0: cards 14rem (224 pixels) wide fit 4 in 1000. */
  async function grid() {
    const fixture = TestBed.createComponent(Host);
    fixture.detectChanges();
    await fixture.whenStable();
    const element: HTMLElement = fixture.nativeElement.querySelector('[appEvenColumns]');
    FakeResizeObserver.last?.resize(1000); // the browser reports the first width once laid out
    return { fixture, element };
  }

  it('waits for the first width before it counts', async () => {
    const fixture = TestBed.createComponent(Host);
    fixture.detectChanges();
    await fixture.whenStable();
    const element: HTMLElement = fixture.nativeElement.querySelector('[appEvenColumns]');
    expect(element.style.getPropertyValue('--columns')).toBe('');
  });

  it('sets the columns that keep the rows even', async () => {
    const { element } = await grid();
    expect(element.style.getPropertyValue('--columns')).toBe('3');
  });

  it('counts again when cards come or go', async () => {
    const { fixture, element } = await grid();
    fixture.componentInstance.cards.update((cards) => [...cards, 'GPU', 'Fans', 'Time']);
    fixture.detectChanges();
    await fixture.whenStable();
    await new Promise((resolve) => setTimeout(resolve)); // the MutationObserver reports later
    expect(element.style.getPropertyValue('--columns')).toBe('4');
  });

  it('counts again when the grid gets a new width', async () => {
    const { element } = await grid();
    FakeResizeObserver.last?.resize(2000);
    expect(element.style.getPropertyValue('--columns')).toBe('5');
  });
});
