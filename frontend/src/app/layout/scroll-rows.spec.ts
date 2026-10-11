import { Component, signal } from '@angular/core';
import { TestBed } from '@angular/core/testing';

import { MAX_ROWS, ScrollRows, moreBelow, rowsHeight } from './scroll-rows';

/** Rows 20 pixels high, one under the other. */
function rows(count: number): { offsetTop: number; offsetHeight: number }[] {
  return Array.from({ length: count }, (_, i) => ({ offsetTop: i * 20, offsetHeight: 20 }));
}

describe('rowsHeight', () => {
  it('keeps the natural height while all rows fit', () => {
    expect(rowsHeight([])).toBeNull();
    expect(rowsHeight(rows(MAX_ROWS))).toBeNull();
  });

  it('ends below the eighth row when there are more', () => {
    expect(rowsHeight(rows(MAX_ROWS + 1))).toBe(160);
    expect(rowsHeight(rows(40))).toBe(160);
  });

  it('counts rows of different heights as they are', () => {
    const mixed = [
      { offsetTop: 0, offsetHeight: 60 },
      { offsetTop: 76, offsetHeight: 40 },
      { offsetTop: 132, offsetHeight: 60 },
    ];
    expect(rowsHeight(mixed, 2)).toBe(116);
  });
});

describe('moreBelow', () => {
  it('tells whether rows are hidden below the visible part', () => {
    expect(moreBelow({ scrollTop: 0, clientHeight: 160, scrollHeight: 400 })).toBe(true);
    expect(moreBelow({ scrollTop: 200, clientHeight: 160, scrollHeight: 400 })).toBe(true);
    expect(moreBelow({ scrollTop: 240, clientHeight: 160, scrollHeight: 400 })).toBe(false);
    // Scrolled to a fraction of a pixel from the end, as zoomed pages do.
    expect(moreBelow({ scrollTop: 239.5, clientHeight: 160, scrollHeight: 400 })).toBe(false);
  });
});

@Component({
  imports: [ScrollRows],
  template: `
    <app-scroll-rows>
      @for (name of names(); track $index) {
        <p class="row">{{ name }}</p>
      }
    </app-scroll-rows>
  `,
})
class Host {
  readonly names = signal(['cpu_thermal']);
}

describe('ScrollRows', () => {
  let scrollTop = 0;

  beforeEach(() => {
    scrollTop = 0;
    vi.stubGlobal(
      'ResizeObserver',
      class {
        observe = vi.fn();
        disconnect = vi.fn();
      },
    );
    // jsdom lays nothing out: each row is 20 pixels high, and the list shows what its max-height lets it.
    vi.spyOn(HTMLElement.prototype, 'offsetHeight', 'get').mockReturnValue(20);
    vi.spyOn(HTMLElement.prototype, 'offsetTop', 'get').mockImplementation(function (
      this: HTMLElement,
    ) {
      return [...(this.parentElement?.children ?? [])].indexOf(this) * 20;
    });
    vi.spyOn(HTMLElement.prototype, 'scrollHeight', 'get').mockImplementation(function (
      this: HTMLElement,
    ) {
      return this.children.length * 20;
    });
    vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get').mockImplementation(function (
      this: HTMLElement,
    ) {
      return parseFloat(this.style.maxHeight) || this.children.length * 20;
    });
    vi.spyOn(HTMLElement.prototype, 'scrollTop', 'get').mockImplementation(() => scrollTop);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  function render(count: number) {
    const fixture = TestBed.createComponent(Host);
    fixture.componentInstance.names.set(Array.from({ length: count }, (_, i) => `sensor ${i}`));
    fixture.detectChanges();
    const element: HTMLElement = fixture.nativeElement;
    return {
      fixture,
      list: element.querySelector<HTMLElement>('.list')!,
      arrow: () => element.querySelector<HTMLButtonElement>('.more'),
    };
  }

  it('keeps up to eight rows as they are, without an arrow', () => {
    const { list, arrow } = render(MAX_ROWS);
    expect(list.style.maxHeight).toBe('');
    expect(arrow()).toBeNull();
  });

  it('shows eight of more rows and an arrow that scrolls down a page', () => {
    const { list, arrow } = render(12);
    expect(list.style.maxHeight).toBe('160px');
    expect(arrow()?.classList).not.toContain('hidden');
    expect(arrow()?.getAttribute('aria-label')).toBe('Scroll down for more');

    list.scrollBy = vi.fn();
    arrow()!.click();
    expect(list.scrollBy).toHaveBeenCalledWith({ top: 160, behavior: 'smooth' });
  });

  it('hides the arrow at the bottom, keeping its room, and shows it again above', () => {
    const { fixture, list, arrow } = render(12);

    scrollTop = 80;
    list.dispatchEvent(new Event('scroll'));
    fixture.detectChanges();
    expect(arrow()?.classList).toContain('hidden');

    scrollTop = 40;
    list.dispatchEvent(new Event('scroll'));
    fixture.detectChanges();
    expect(arrow()?.classList).not.toContain('hidden');
  });

  it('lets the list grow again when rows go', () => {
    const { fixture, list, arrow } = render(12);
    fixture.componentInstance.names.set(['cpu_thermal', 'nvme Composite']);
    fixture.detectChanges();
    expect(list.style.maxHeight).toBe('');
    expect(arrow()).toBeNull();
  });
});
