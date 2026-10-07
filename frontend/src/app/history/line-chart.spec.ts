import { ComponentFixture, TestBed } from '@angular/core/testing';

import { ChartLine, LineChart, niceCeiling } from './line-chart';

describe('LineChart', () => {
  let fixture: ComponentFixture<LineChart>;

  function render(lines: ChartLine[]): HTMLElement {
    fixture = TestBed.createComponent(LineChart);
    fixture.componentRef.setInput('title', 'CPU and memory');
    fixture.componentRef.setInput('lines', lines);
    fixture.componentRef.setInput('from', 0);
    fixture.componentRef.setInput('to', 600);
    fixture.componentRef.setInput('step', 60);
    fixture.componentRef.setInput('unit', 'percent');
    fixture.componentRef.setInput('max', 100);
    fixture.detectChanges();
    return fixture.nativeElement as HTMLElement;
  }

  it('draws one path per line and leaves a gap where values are missing', () => {
    const element = render([
      {
        label: 'CPU',
        points: [
          { time: 0, value: 0 },
          { time: 60, value: 50 },
          { time: 300, value: 100 },
        ],
      },
    ]);

    const paths = element.querySelectorAll('path');
    expect(paths.length).toBe(1);
    // The first two points are joined; the third starts a new segment after the gap.
    expect(paths[0].getAttribute('d')).toBe('M50.0 100.0h0L150.0 50.0M550.0 0.0h0');
  });

  it('shows a legend only when there are several lines', () => {
    const single = render([{ label: 'CPU', points: [] }]);
    expect(single.querySelector('.legend')).toBeNull();

    const several = render([
      { label: 'CPU', points: [] },
      { label: 'Memory', points: [] },
    ]);
    expect(several.querySelector('.legend')?.textContent).toContain('Memory');
  });

  it('shows the values at the pointer', () => {
    const element = render([
      { label: 'CPU', points: [{ time: 120, value: 12.5 }] },
      { label: 'Memory', points: [] },
    ]);
    const plot = element.querySelector('.plot') as HTMLElement;
    plot.getBoundingClientRect = () => ({ left: 0, width: 600 }) as DOMRect;

    plot.dispatchEvent(new MouseEvent('pointermove', { clientX: 150 }));
    fixture.detectChanges();

    const tooltip = element.querySelector('.tooltip')?.textContent ?? '';
    expect(tooltip).toContain('12.5 %');
    expect(tooltip).toContain('–');

    plot.dispatchEvent(new MouseEvent('pointerleave'));
    fixture.detectChanges();
    expect(element.querySelector('.tooltip')).toBeNull();
  });

  it('shows the values of the last step at the right edge', () => {
    const element = render([{ label: 'CPU', points: [{ time: 540, value: 30 }] }]);
    const plot = element.querySelector('.plot') as HTMLElement;
    plot.getBoundingClientRect = () => ({ left: 0, width: 600 }) as DOMRect;

    plot.dispatchEvent(new MouseEvent('pointermove', { clientX: 600 }));
    fixture.detectChanges();

    expect(element.querySelector('.tooltip')?.textContent).toContain('30 %');
  });

  it('tells screen readers what the chart shows', () => {
    const element = render([
      { label: 'CPU', points: [] },
      { label: 'Memory', points: [] },
    ]);

    const label = element.querySelector('svg')?.getAttribute('aria-label') ?? '';
    expect(element.querySelector('svg')?.getAttribute('role')).toBe('img');
    expect(label).toMatch(/^Chart of CPU and memory from .+ to .+: CPU and Memory$/);
  });

  it('dashes the lines after the eighth and keeps lines of the same name apart', () => {
    const lines = () => Array.from({ length: 9 }, () => ({ label: 'sda', points: [] }));
    const element = render(lines());
    // Angular warns when a list it updates repeats a key.
    const warn = vi.spyOn(console, 'warn');
    fixture.componentRef.setInput('lines', lines());
    fixture.detectChanges();
    expect(warn).not.toHaveBeenCalled();

    const swatches = element.querySelectorAll('.legend .swatch');
    expect(swatches.length).toBe(9);
    expect(swatches[0].classList.contains('dashed')).toBe(false);
    expect([...swatches[8].classList].sort()).toEqual(['dashed', 'series-0', 'swatch']);
    expect(element.querySelectorAll('path.dashed').length).toBe(1);
  });
});

describe('niceCeiling', () => {
  it('rounds temperatures up to the next ten degrees', () => {
    expect(niceCeiling(61, 'celsius')).toBe(70);
    expect(niceCeiling(0, 'celsius')).toBe(10);
  });

  it('rounds other values up to 1, 2 or 5 times a power of ten', () => {
    expect(niceCeiling(3.2, 'watts')).toBe(5);
    expect(niceCeiling(140, 'number')).toBe(200);
    expect(niceCeiling(0, 'perSecond')).toBe(1);
  });

  it('rounds network speeds up to a power of two', () => {
    expect(niceCeiling(300 * 1024, 'bytesPerSecond')).toBe(512 * 1024);
    expect(niceCeiling(0, 'bytesPerSecond')).toBe(1024);
  });
});
