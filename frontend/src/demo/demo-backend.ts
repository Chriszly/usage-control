import {
  HttpBackend,
  HttpErrorResponse,
  HttpEvent,
  HttpRequest,
  HttpResponse,
} from '@angular/common/http';
import { Injectable } from '@angular/core';
import { Observable, delay, mergeMap, of, throwError, timer } from 'rxjs';

import { Availability, DeviceList, LOCAL_DEVICE, Suggestion } from '../app/devices/devices';
import { History, Series } from '../app/metrics/metrics';
import { DemoMachine, FLEET, offlineSince, snapshotOf } from './fleet';

/** How many days of history the demo hub keeps; more than 30 shows the "All" range. */
export const DEMO_RETENTION_DAYS = 90;

/** Like the backend: readings every 5 s for the last 30 minutes, minute averages before that. */
const RECENT_SPAN = 30 * 60;
const RECENT_INTERVAL = 5;
const SAMPLE_INTERVAL = 60;
const MAX_POINTS = 360;

/** The visitor's device, which the Devices dialog offers to add like on a real hub. */
const DEMO_SUGGESTION: Suggestion = {
  address: '192.168.1.47:9393',
  name: 'Kitchen tablet',
  kind: 'pc',
};

/** How long the made-up devices take to answer, so the page loads like it does on a real hub. */
const LATENCY_MS = 60;

/**
 * Answers the page's requests to /api with made-up values from the sample
 * devices instead of sending them anywhere. Devices cannot be added, removed or changed.
 */
@Injectable()
export class DemoBackend implements HttpBackend {
  handle(request: HttpRequest<unknown>): Observable<HttpEvent<unknown>> {
    const url = new URL(request.urlWithParams, 'http://demo');
    const device = url.searchParams.get('device') ?? LOCAL_DEVICE.id;
    const machine = FLEET.find((m) => m.device.id === device);
    const now = Date.now() / 1000;

    if (request.method === 'GET' && url.pathname === '/api/devices') {
      return respond(request, deviceList());
    }
    if (request.method === 'GET' && url.pathname === '/api/devices/suggestion') {
      return respond(request, DEMO_SUGGESTION);
    }
    if (request.method === 'POST' || request.method === 'PUT' || request.method === 'DELETE') {
      return fail(request, 403, { problem: 'demo' });
    }
    if (!machine) {
      return fail(request, 404, 'unknown device');
    }
    switch (url.pathname) {
      case '/api/metrics':
        return isOnline(machine, now)
          ? respond(request, snapshotOf(machine, Math.floor(now)))
          : fail(request, 503, 'the device has not answered recently');
      case '/api/history':
        return respond(
          request,
          history(
            machine,
            Number(url.searchParams.get('from')),
            Number(url.searchParams.get('to')),
          ),
        );
      case '/api/availability':
        return respond(request, availability(machine, now));
      default:
        // No /api/update: the demo names no newer release.
        return fail(request, 404, 'not found');
    }
  }
}

function deviceList(): DeviceList {
  return { devices: FLEET.map((m) => m.device), passwordSet: true };
}

function isOnline(machine: DemoMachine, t: number): boolean {
  return machine.online?.(t) ?? true;
}

/** The averages from `from` up to `to` in the steps the backend would use. */
export function history(machine: DemoMachine, from: number, to: number): History {
  const span = Math.max(1, to - from);
  const interval = span <= RECENT_SPAN ? RECENT_INTERVAL : SAMPLE_INTERVAL;
  let step = Math.max(1, Math.ceil(span / interval / MAX_POINTS)) * interval;
  if (step >= 3600) {
    step = Math.ceil(step / 3600) * 3600;
  }
  const oldest = Math.max(from, to - DEMO_RETENTION_DAYS * 86400);
  const points = new Map<string, { time: number; value: number }[]>();
  // A long step averages several moments, like the backend's averages smooth out short peaks.
  const samples = Math.min(8, Math.max(1, Math.round(step / RECENT_INTERVAL)));
  for (let start = Math.ceil(oldest / step) * step; start < to; start += step) {
    const moments = Array.from(
      { length: samples },
      (_, i) => start + ((i + 0.5) * step) / samples,
    ).filter((t) => t <= to && isOnline(machine, t));
    if (moments.length === 0) {
      continue;
    }
    const totals = new Map<string, number>();
    for (const t of moments) {
      for (const [metric, value] of Object.entries(machine.values(t, step))) {
        totals.set(metric, (totals.get(metric) ?? 0) + value);
      }
    }
    for (const [metric, total] of totals) {
      const line = points.get(metric) ?? [];
      line.push({ time: start, value: total / moments.length });
      points.set(metric, line);
    }
  }
  const series: Series[] = [...points].map(([metric, values]) => ({ metric, points: values }));
  return { from, to, stepSeconds: step, retentionDays: DEMO_RETENTION_DAYS, series };
}

/**
 * Every device was added 60 days ago; only the laptop that sleeps at night was ever off, which
 * the page shows as the time it was not in use, since it is a PC.
 */
function availability(machine: DemoMachine, now: number): Availability {
  const since = now - 60 * 86400;
  const kind = machine.device.kind ?? 'server';
  const online = machine.online;
  if (!online) {
    return { kind, since: iso(since), offlineSeconds: 0, outages: 0 };
  }
  let offlineSeconds = 0;
  let outages = 0;
  let wasOnline = true;
  for (let t = since; t < now; t += 300) {
    const answered = online(t);
    if (!answered) {
      offlineSeconds += 300;
      outages += wasOnline ? 1 : 0;
    }
    wasOnline = answered;
  }
  return {
    kind,
    since: iso(since),
    offlineSeconds,
    outages,
    lastOutage: { start: iso(offlineSince(online, now)), end: iso(now) },
  };
}

function iso(t: number): string {
  return new Date(t * 1000).toISOString();
}

function respond<T>(request: HttpRequest<unknown>, body: T): Observable<HttpEvent<T>> {
  return of(new HttpResponse({ body, status: 200, url: request.url })).pipe(delay(LATENCY_MS));
}

function fail(request: HttpRequest<unknown>, status: number, error: unknown): Observable<never> {
  // delay() lets errors through at once, so the error is thrown after a timer instead.
  return timer(LATENCY_MS).pipe(
    mergeMap(() => throwError(() => new HttpErrorResponse({ error, status, url: request.url }))),
  );
}
