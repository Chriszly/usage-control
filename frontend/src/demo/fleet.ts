import { Device, LOCAL_DEVICE } from '../app/devices/devices';
import { Extra, ExtraInfo, ExtraUnit } from '../app/metrics/extras';
import { Snapshot, Throttling, TimeZone } from '../app/metrics/metrics';

/**
 * The made-up devices of the demo: a Raspberry Pi as the hub, and Windows and
 * Linux PCs and servers on the same network. Each one reports what the real
 * backend reports on that kind of machine, from values that drift smoothly
 * over time, so the live values and the history agree.
 */

const GB = 1024 ** 3;
const TB = 1024 ** 4;

/** The values of one moment, keyed like the history's series: "cpu", "disk:/", "network.receive:eth0" and so on. */
export type Values = Record<string, number>;

export interface DemoMachine {
  device: Device;
  os: 'linux' | 'windows';
  cores: number;
  maxClockMHz: number;
  memoryBytes: number;
  swapBytes?: number;
  disks: { path: string; totalBytes: number }[];
  /** Interfaces without traffic values are listed as idle, like the extra adapters of Windows PCs. */
  network: { name: string; addresses?: string[]; linkMbps?: number }[];
  gpus?: { name: string; memoryBytes?: number }[];
  fans?: string[];
  /** Values beyond the fixed ones; each value comes from values() as "extra:<group>/<value>", texts are fixed. */
  extras?: Extra[];
  throttling?: Throttling;
  /** Linux laptops report the battery's power and health too. */
  batteryDetails?: boolean;
  /** Servers keep their clocks in UTC; the other devices use the visitor's time zone. */
  utc?: boolean;
  bootedDaysAgo: number;
  /** For a device that answers only part of the day, such as a laptop that sleeps at night. */
  online?: (t: number) => boolean;
  /**
   * The values at t, in Unix seconds. With a step, as for a long history range, changes
   * much faster than the step are left out, since averages over the step smooth them out.
   */
  values: (t: number, step: number) => Values;
}

/** When the page was opened, in Unix seconds. */
const openedAt = Math.floor(Date.now() / 1000);

/** A number from 0 to 1 for each whole number i, the same every time. */
function hash(i: number, seed: number): number {
  let h = Math.imul(i ^ Math.imul(seed, 0x9e3779b1), 0x27d4eb2d);
  h ^= h >>> 15;
  h = Math.imul(h, 0x85ebca6b);
  h ^= h >>> 13;
  return (h >>> 0) / 4294967296;
}

/** A value from -1 to 1 that changes smoothly, about once per period seconds. */
export function drift(t: number, period: number, seed: number): number {
  const x = t / period;
  const i = Math.floor(x);
  const f = x - i;
  const s = f * f * (3 - 2 * f);
  return (hash(i, seed) * (1 - s) + hash(i + 1, seed) * s) * 2 - 1;
}

/** base plus several drifts, each [amplitude, period], kept within min and max. */
function vary(
  t: number,
  step: number,
  seed: number,
  base: number,
  swings: [number, number][],
  min = 0,
  max = 100,
): number {
  const value = swings.reduce(
    (sum, [amplitude, period], i) =>
      sum + amplitude * Math.min(1, period / Math.max(1, step)) * drift(t, period, seed * 31 + i),
    base,
  );
  return Math.min(max, Math.max(min, value));
}

/** The hour of the day at t, from 0 to 24, in the visitor's time zone. */
function hourOf(t: number): number {
  return ((((t - new Date(t * 1000).getTimezoneOffset() * 60) / 3600) % 24) + 24) % 24;
}

/** 1 in the middle of the working day and 0 at night. */
function workday(t: number): number {
  return Math.max(0, Math.sin(((hourOf(t) - 7) / 12) * Math.PI));
}

/** A battery that runs down over about four hours and charges in about one and a half. */
function batteryPercent(t: number): number {
  const cycle = 5.5 * 3600;
  const phase = (((t + 9000) % cycle) + cycle) % cycle;
  const discharge = 4 * 3600;
  return phase < discharge
    ? 98 - (phase / discharge) * 78
    : 20 + ((phase - discharge) / (cycle - discharge)) * 78;
}

function charging(t: number): boolean {
  return batteryPercent(t + 60) > batteryPercent(t);
}

const cpuPackageLabels = { de: 'CPU-Paket 0', fr: 'Processeur 0', es: 'Procesador 0' };
const memoryLabels = { de: 'Arbeitsspeicher', fr: 'Mémoire', es: 'Memoria' };

/** What the power add-on reports, with the given values; their watts come from values() as "extra:power/<id>". */
function powerAddOn(
  items: { id: string; label: string; labels?: Record<string, string> }[],
): Extra[] {
  return [
    {
      id: 'power',
      title: 'Power',
      titles: { de: 'Leistungsaufnahme', fr: 'Consommation', es: 'Consumo' },
      items: items.map((item) => ({ ...item, unit: 'watts', history: true })),
    },
  ];
}

/** What the pressure add-on reports; its percentages come from values() as "extra:pressure/<id>". */
function pressureAddOn(): Extra[] {
  const items: { id: string; label: string; labels: Record<string, string> }[] = [
    {
      id: 'cpu-some',
      label: 'CPU: tasks waiting',
      labels: {
        de: 'CPU: Prozesse warten',
        fr: 'Processeur : tâches en attente',
        es: 'CPU: tareas en espera',
      },
    },
    {
      id: 'memory-some',
      label: 'Memory: tasks waiting',
      labels: {
        de: 'Arbeitsspeicher: Prozesse warten',
        fr: 'Mémoire : tâches en attente',
        es: 'Memoria: tareas en espera',
      },
    },
    {
      id: 'memory-full',
      label: 'Memory: all tasks stalled',
      labels: {
        de: 'Arbeitsspeicher: alle Prozesse blockiert',
        fr: 'Mémoire : toutes les tâches bloquées',
        es: 'Memoria: todas las tareas bloqueadas',
      },
    },
    {
      id: 'io-some',
      label: 'Disks and I/O: tasks waiting',
      labels: {
        de: 'Datenträger und E/A: Prozesse warten',
        fr: 'Disques et E/S : tâches en attente',
        es: 'Discos y E/S: tareas en espera',
      },
    },
    {
      id: 'io-full',
      label: 'Disks and I/O: all tasks stalled',
      labels: {
        de: 'Datenträger und E/A: alle Prozesse blockiert',
        fr: 'Disques et E/S : toutes les tâches bloquées',
        es: 'Discos y E/S: todas las tareas bloqueadas',
      },
    },
  ];
  return [
    {
      id: 'pressure',
      title: 'Pressure',
      titles: { de: 'Engpässe', fr: 'Saturation', es: 'Saturación' },
      items: items.map((item) => ({ ...item, unit: 'percent', history: true })),
    },
  ];
}

/** What the gpu add-on reports of one NVIDIA GPU; its values come from gpuAddOnValues(). */
function gpuAddOn(performanceState: string): Extra[] {
  const item = (
    id: string,
    label: string,
    labels: Record<string, string>,
    unit: ExtraUnit,
    history: boolean,
  ) => ({ id: `0-${id}`, label, labels, unit, history });
  return [
    {
      id: 'gpu',
      title: 'Graphics card',
      titles: { de: 'Grafikkarte', fr: 'Carte graphique', es: 'Tarjeta gráfica' },
      items: [
        item('fan', 'Fan', { de: 'Lüfter', fr: 'Ventilateur', es: 'Ventilador' }, 'percent', true),
        item(
          'graphics-clock',
          'Graphics clock (MHz)',
          {
            de: 'Grafiktakt (MHz)',
            fr: 'Fréquence graphique (MHz)',
            es: 'Frecuencia gráfica (MHz)',
          },
          'number',
          true,
        ),
        item(
          'memory-clock',
          'Memory clock (MHz)',
          {
            de: 'Speichertakt (MHz)',
            fr: 'Fréquence mémoire (MHz)',
            es: 'Frecuencia de memoria (MHz)',
          },
          'number',
          true,
        ),
        item(
          'encoder',
          'Video encoder',
          { de: 'Video-Encoder', fr: 'Encodeur vidéo', es: 'Codificador de vídeo' },
          'percent',
          true,
        ),
        item(
          'decoder',
          'Video decoder',
          { de: 'Video-Decoder', fr: 'Décodeur vidéo', es: 'Decodificador de vídeo' },
          'percent',
          true,
        ),
        {
          ...item(
            'performance-state',
            'Performance state',
            { de: 'Leistungszustand', fr: 'État de performance', es: 'Estado de rendimiento' },
            'text',
            false,
          ),
          text: performanceState,
        },
        item(
          'power-limit',
          'Power limit',
          { de: 'Leistungsgrenze', fr: 'Limite de puissance', es: 'Límite de potencia' },
          'watts',
          false,
        ),
      ],
    },
  ];
}

/** What the inodes add-on reports for the given mount points; their percentages come from values() as "extra:inodes/<id>". */
function inodesAddOn(items: { id: string; label: string }[]): Extra[] {
  return [
    {
      id: 'inodes',
      title: 'Inodes (files) in use',
      titles: {
        de: 'Belegte Inodes (Dateien)',
        fr: 'Inodes (fichiers) utilisés',
        es: 'Inodos (archivos) en uso',
      },
      items: items.map((item) => ({ ...item, unit: 'percent', history: true })),
    },
  ];
}

/** The I/O pressure of pressureAddOn(), with "all tasks stalled" a share of "tasks waiting", whose time it is part of. */
function ioPressure(some: number, fullShare: number): Record<string, number> {
  return { 'extra:pressure/io-some': some, 'extra:pressure/io-full': some * fullShare };
}

/** The values of gpuAddOn() at a GPU usage of gpu percent. */
function gpuAddOnValues(
  gpu: number,
  maxClockMHz: number,
  memoryClockMHz: number,
  powerLimitWatts: number,
  video: number,
): Record<string, number> {
  return {
    'extra:gpu/0-fan': gpu < 15 ? 0 : 30 + gpu * 0.4,
    'extra:gpu/0-graphics-clock': Math.round(210 + (maxClockMHz - 210) * Math.min(1, gpu / 40)),
    'extra:gpu/0-memory-clock': gpu < 5 ? 405 : memoryClockMHz,
    'extra:gpu/0-encoder': video,
    'extra:gpu/0-decoder': video * 0.6,
    'extra:gpu/0-power-limit': powerLimitWatts,
  };
}

/** What the kernel add-on reports; its values come from values() as "extra:kernel/<id>", from kernelValues(). */
function kernelAddOn(): Extra[] {
  const perSecond = (id: string, label: string, labels: Record<string, string>) => ({
    id,
    label,
    labels,
    unit: 'perSecond' as const,
    history: true,
  });
  const number = (id: string, label: string, labels: Record<string, string>) => ({
    id,
    label,
    labels,
    unit: 'number' as const,
    history: true,
  });
  return [
    {
      id: 'kernel',
      title: 'Kernel',
      titles: { de: 'Kernel', fr: 'Noyau', es: 'Núcleo' },
      items: [
        perSecond('context-switches', 'Context switches', {
          de: 'Kontextwechsel',
          fr: 'Changements de contexte',
          es: 'Cambios de contexto',
        }),
        perSecond('interrupts', 'Interrupts', {
          de: 'Interrupts',
          fr: 'Interruptions',
          es: 'Interrupciones',
        }),
        perSecond('new-processes', 'New processes and threads', {
          de: 'Neue Prozesse und Threads',
          fr: 'Nouveaux processus et threads',
          es: 'Procesos e hilos nuevos',
        }),
        number('open-files', 'Open files', {
          de: 'Offene Dateien',
          fr: 'Fichiers ouverts',
          es: 'Archivos abiertos',
        }),
        number('sockets', 'Sockets in use', {
          de: 'Belegte Sockets',
          fr: 'Sockets utilisés',
          es: 'Sockets en uso',
        }),
        number('tcp-established', 'Established TCP connections', {
          de: 'Aufgebaute TCP-Verbindungen',
          fr: 'Connexions TCP établies',
          es: 'Conexiones TCP establecidas',
        }),
        perSecond('tcp-retransmissions', 'TCP retransmissions', {
          de: 'TCP-Neuübertragungen',
          fr: 'Retransmissions TCP',
          es: 'Retransmisiones TCP',
        }),
      ],
    },
  ];
}

/** The kernel add-on's values at t for a machine with the given CPU usage, scaled by size (1 for a Raspberry Pi). */
function kernelValues(t: number, step: number, seed: number, cpu: number, size: number): Values {
  const count = (base: number, swing: number, period: number, i: number) =>
    Math.round(vary(t, step, seed + i, base * size, [[swing * size, period]], 0, 1e9));
  return {
    'extra:kernel/context-switches': (900 + cpu * 60) * size + count(0, 200, 30, 0),
    'extra:kernel/interrupts': (700 + cpu * 35) * size + count(0, 150, 30, 1),
    'extra:kernel/new-processes': vary(t, step, seed + 2, 1 + cpu * 0.08, [[1, 60]], 0, 1e6) * size,
    'extra:kernel/open-files': count(1400, 150, 3600, 3),
    'extra:kernel/sockets': count(160, 20, 1800, 4),
    'extra:kernel/tcp-established': count(12, 6, 900, 5),
    'extra:kernel/tcp-retransmissions': vary(t, step, seed + 6, 0.2, [[0.3, 120]], 0, 1e6) * size,
  };
}

/**
 * What the Wi-Fi add-on reports for the interface name: on Linux its name, which is its id too, on Windows the
 * adapter's description, with the interface's GUID as id. Its values come from values() as "extra:wifi/<id>-quality"
 * and "-signal"; only the quality keeps its history, as the signal is below 0.
 */
function wifiAddOn(name: string, id = name): Extra[] {
  // Like the add-on, a name too long for a label of 80 characters is cut so what the value is stays whole.
  const label = (what: string) =>
    name.length + 1 + what.length <= 80
      ? `${name} ${what}`
      : `${name.slice(0, 78 - what.length).trimEnd()}… ${what}`;
  return [
    {
      id: 'wifi',
      title: 'Wi-Fi',
      titles: { de: 'WLAN', fr: 'Wi-Fi', es: 'Wi-Fi' },
      items: [
        {
          id: `${id}-quality`,
          label: label('link quality'),
          labels: {
            de: label('Verbindungsqualität'),
            fr: label('qualité du lien'),
            es: label('calidad del enlace'),
          },
          unit: 'percent',
          history: true,
        },
        {
          id: `${id}-signal`,
          label: label('signal (dBm)'),
          labels: {
            de: label('Signal (dBm)'),
            fr: label('signal (dBm)'),
            es: label('señal (dBm)'),
          },
          unit: 'number',
        },
      ],
    },
  ];
}

/** What the memory add-on reports; its values come from memoryValues() as "extra:memory/<id>". */
function memoryAddOn(): Extra[] {
  const bytes = (id: string, label: string, labels: Record<string, string>) => ({
    id,
    label,
    labels,
    unit: 'bytes' as const,
    history: true,
  });
  return [
    {
      id: 'memory',
      title: 'Memory details',
      titles: { de: 'Speicherdetails', fr: 'Détails de la mémoire', es: 'Detalles de la memoria' },
      items: [
        bytes('dirty', 'Dirty (waiting to be written)', {
          de: 'Ungeschrieben (Dirty)',
          fr: 'Modifiée, pas encore écrite (Dirty)',
          es: 'Modificada, sin escribir (Dirty)',
        }),
        bytes('writeback', 'Being written back', {
          de: 'Wird geschrieben (Writeback)',
          fr: "En cours d'écriture (Writeback)",
          es: 'Escribiéndose (Writeback)',
        }),
        bytes('slab', 'Kernel caches (slab)', {
          de: 'Kernel-Caches (Slab)',
          fr: 'Caches du noyau (slab)',
          es: 'Cachés del núcleo (slab)',
        }),
        bytes('shared', 'Shared memory', {
          de: 'Gemeinsamer Speicher',
          fr: 'Mémoire partagée',
          es: 'Memoria compartida',
        }),
        bytes('page-tables', 'Page tables', {
          de: 'Seitentabellen',
          fr: 'Tables de pages',
          es: 'Tablas de páginas',
        }),
        bytes('committed', 'Committed', {
          de: 'Zugesagt (Committed)',
          fr: 'Engagée (Committed)',
          es: 'Comprometida (Committed)',
        }),
        {
          id: 'page-faults',
          label: 'Page faults',
          labels: { de: 'Seitenfehler', fr: 'Défauts de page', es: 'Fallos de página' },
          unit: 'perSecond',
          history: true,
        },
        {
          id: 'major-page-faults',
          label: 'Major page faults (read from disk)',
          labels: {
            de: 'Schwere Seitenfehler (von der Platte)',
            fr: 'Défauts de page majeurs (lus sur disque)',
            es: 'Fallos de página mayores (leídos del disco)',
          },
          unit: 'perSecond',
          history: true,
        },
        {
          id: 'swap-in',
          label: 'Swapped in',
          labels: { de: 'Aus dem Swap gelesen', fr: 'Lu depuis le swap', es: 'Leído del swap' },
          unit: 'bytesPerSecond',
          history: true,
        },
        {
          id: 'swap-out',
          label: 'Swapped out',
          labels: {
            de: 'In den Swap geschrieben',
            fr: 'Écrit dans le swap',
            es: 'Escrito en el swap',
          },
          unit: 'bytesPerSecond',
          history: true,
        },
      ],
    },
  ];
}

/**
 * The memory add-on's values for a machine with memoryBytes of memory, busy
 * by load (0 to 1), from seeds seed to seed + 9.
 */
function memoryValues(
  t: number,
  step: number,
  seed: number,
  memoryBytes: number,
  load: number,
): Record<string, number> {
  const m = memoryBytes;
  return {
    'extra:memory/dirty': vary(t, step, seed, m * (0.0005 + 0.004 * load), [[m * 0.001, 30]], 0, m),
    'extra:memory/writeback': vary(t, step, seed + 1, m * 0.0002 * load, [[m * 0.0002, 20]], 0, m),
    'extra:memory/slab': vary(t, step, seed + 2, m * 0.03, [[m * 0.005, 3600]], 0, m),
    'extra:memory/shared': vary(t, step, seed + 3, m * 0.01, [[m * 0.003, 1800]], 0, m),
    'extra:memory/page-tables': vary(
      t,
      step,
      seed + 4,
      m * 0.002 * (1 + load),
      [[m * 0.0005, 600]],
      0,
      m,
    ),
    'extra:memory/committed': vary(
      t,
      step,
      seed + 5,
      m * (0.4 + 0.3 * load),
      [[m * 0.05, 900]],
      0,
      2 * m,
    ),
    'extra:memory/page-faults': vary(t, step, seed + 6, 800 + 40e3 * load, [[600, 30]], 0, 1e7),
    'extra:memory/major-page-faults': vary(t, step, seed + 7, 0.5 + 20 * load, [[1, 60]], 0, 1e5),
    'extra:memory/swap-in': vary(t, step, seed + 8, 0, [[4e3, 300]], 0, 1e9),
    'extra:memory/swap-out': vary(t, step, seed + 9, 0, [[6e3, 600]], 0, 1e9),
  };
}

/**
 * What the memory add-on reports on Windows, from the Memory performance
 * counters; its values come from windowsMemoryValues().
 */
function windowsMemoryAddOn(): Extra[] {
  const item = (
    id: string,
    label: string,
    labels: Record<string, string>,
    unit: 'bytes' | 'perSecond' | 'bytesPerSecond',
  ) => ({ id, label, labels, unit, history: true });
  return [
    {
      id: 'memory',
      title: 'Memory details',
      titles: { de: 'Speicherdetails', fr: 'Détails de la mémoire', es: 'Detalles de la memoria' },
      items: [
        item(
          'modified',
          'Modified (waiting to be written)',
          {
            de: 'Geändert, ungeschrieben (Modified)',
            fr: 'Modifiée, pas encore écrite (Modified)',
            es: 'Modificada, sin escribir (Modified)',
          },
          'bytes',
        ),
        item(
          'pool-paged',
          'Kernel paged pool',
          {
            de: 'Kernel-Pool, auslagerbar',
            fr: 'Pool paginé du noyau',
            es: 'Bloque paginado del núcleo',
          },
          'bytes',
        ),
        item(
          'pool-nonpaged',
          'Kernel nonpaged pool',
          {
            de: 'Kernel-Pool, nicht auslagerbar',
            fr: 'Pool non paginé du noyau',
            es: 'Bloque no paginado del núcleo',
          },
          'bytes',
        ),
        item(
          'committed',
          'Committed',
          {
            de: 'Zugesagt (Committed)',
            fr: 'Engagée (Committed)',
            es: 'Comprometida (Committed)',
          },
          'bytes',
        ),
        item(
          'page-faults',
          'Page faults',
          { de: 'Seitenfehler', fr: 'Défauts de page', es: 'Fallos de página' },
          'perSecond',
        ),
        item(
          'page-reads',
          'Disk reads for page faults',
          {
            de: 'Lesezugriffe für Seitenfehler',
            fr: 'Lectures disque pour défauts de page',
            es: 'Lecturas de disco por fallos de página',
          },
          'perSecond',
        ),
        item(
          'paged-in',
          'Paged in (page file and mapped files)',
          {
            de: 'Eingelagert (Auslagerungsdatei und Dateien)',
            fr: "Pages lues (fichier d'échange et fichiers)",
            es: 'Páginas leídas (archivo de paginación y archivos)',
          },
          'bytesPerSecond',
        ),
        item(
          'paged-out',
          'Paged out (page file and mapped files)',
          {
            de: 'Ausgelagert (Auslagerungsdatei und Dateien)',
            fr: "Pages écrites (fichier d'échange et fichiers)",
            es: 'Páginas escritas (archivo de paginación y archivos)',
          },
          'bytesPerSecond',
        ),
      ],
    },
  ];
}

/**
 * The Windows memory add-on's values for a machine with memoryBytes of
 * memory, busy by load (0 to 1), from seeds seed to seed + 7.
 */
function windowsMemoryValues(
  t: number,
  step: number,
  seed: number,
  memoryBytes: number,
  load: number,
): Record<string, number> {
  const m = memoryBytes;
  return {
    'extra:memory/modified': vary(
      t,
      step,
      seed,
      m * (0.002 + 0.006 * load),
      [[m * 0.002, 60]],
      0,
      m,
    ),
    'extra:memory/pool-paged': vary(t, step, seed + 1, m * 0.008, [[m * 0.001, 3600]], 0, m),
    'extra:memory/pool-nonpaged': vary(t, step, seed + 2, m * 0.004, [[m * 0.0005, 3600]], 0, m),
    'extra:memory/committed': vary(
      t,
      step,
      seed + 3,
      m * (0.5 + 0.3 * load),
      [[m * 0.05, 900]],
      0,
      2 * m,
    ),
    'extra:memory/page-faults': vary(t, step, seed + 4, 2e3 + 30e3 * load, [[1500, 30]], 0, 1e7),
    'extra:memory/page-reads': vary(t, step, seed + 5, 2 + 30 * load, [[3, 60]], 0, 1e5),
    'extra:memory/paged-in': vary(t, step, seed + 6, 50e3 + 2e6 * load, [[200e3, 120]], 0, 1e9),
    'extra:memory/paged-out': vary(t, step, seed + 7, 10e3 * load, [[30e3, 600]], 0, 1e9),
  };
}

const portsLabels = { de: 'Offene Ports', fr: 'Ports en écoute', es: 'Puertos en escucha' };

/**
 * What the ports add-on reports for the given ports, each as protocol, number
 * and addresses; their count comes from values() as "extra:ports/count".
 */
function portsAddOn(ports: ['tcp' | 'udp', number, string][]): Extra[] {
  return [
    {
      id: 'ports',
      title: 'Listening ports',
      titles: portsLabels,
      items: [
        {
          id: 'count',
          label: 'Listening ports',
          labels: portsLabels,
          unit: 'number',
          history: true,
        },
        ...ports.map(([protocol, port, addresses]) => ({
          id: `${protocol}-${port}`,
          label: `${protocol.toUpperCase()} ${port}`,
          unit: 'text' as const,
          text: addresses,
        })),
      ],
    },
  ];
}

/** The values the smart add-on reports for a disk, with their unit and labels as the add-on writes them. */
const smartValues = {
  temperature: {
    label: 'Temperature',
    labels: { de: 'Temperatur', fr: 'Température', es: 'Temperatura' },
    unit: 'celsius',
  },
  'power-on-hours': {
    label: 'Power-on hours',
    labels: {
      de: 'Betriebsstunden',
      fr: 'Heures de fonctionnement',
      es: 'Horas de funcionamiento',
    },
    unit: 'number',
  },
  reallocated: {
    label: 'Reallocated sectors',
    labels: { de: 'Ersetzte Sektoren', fr: 'Secteurs réalloués', es: 'Sectores reasignados' },
    unit: 'number',
  },
  'media-errors': {
    label: 'Media errors',
    labels: { de: 'Medienfehler', fr: 'Erreurs de support', es: 'Errores de medio' },
    unit: 'number',
  },
  used: {
    label: 'Wear',
    labels: { de: 'Verschleiß', fr: 'Usure', es: 'Desgaste' },
    unit: 'percent',
  },
} as const;

const passedLabels = {
  de: 'SMART-Prüfung bestanden',
  fr: 'Contrôle SMART réussi',
  es: 'Comprobación SMART superada',
};

/** Puts the disk before a label and each of its translations, as the smart add-on does. */
function diskLabels(disk: string, label: string, labels: Record<string, string>) {
  return {
    label: `${disk}: ${label}`,
    labels: Object.fromEntries(Object.entries(labels).map(([code, l]) => [code, `${disk}: ${l}`])),
  };
}

/**
 * What the smart add-on reports for the given disks: whether each one passes its own check, and
 * the numbers it reports, which come from values() as "extra:smart/<disk id>-<value>". The id is
 * the disk's serial number, as the add-on keys each disk on it.
 */
function smartAddOn(
  disks: { id: string; name: string; values: (keyof typeof smartValues)[] }[],
): Extra[] {
  return [
    {
      id: 'smart',
      title: 'Disk health',
      titles: { de: 'Laufwerkszustand', fr: 'Santé des disques', es: 'Salud de los discos' },
      items: disks.flatMap((disk) => [
        {
          id: `${disk.id}-health`,
          ...diskLabels(disk.name, 'SMART check passed', passedLabels),
          unit: 'text' as const,
          text: '✓',
        },
        ...disk.values.map((value) => ({
          id: `${disk.id}-${value}`,
          ...diskLabels(disk.name, smartValues[value].label, smartValues[value].labels),
          unit: smartValues[value].unit,
          history: true,
        })),
      ]),
    },
  ];
}

/**
 * What the containers add-on reports for the given containers, each under its name; their
 * values come from values() as "extra:containers-cpu/<name>" in percent and
 * "extra:containers-memory/<name>" in bytes.
 */
function containersAddOn(names: string[]): Extra[] {
  return [
    {
      id: 'containers-cpu',
      title: 'Containers: CPU',
      titles: { de: 'Container: CPU', fr: 'Conteneurs : processeur', es: 'Contenedores: CPU' },
      items: names.map((name) => ({ id: name, label: name, unit: 'percent', history: true })),
    },
    {
      id: 'containers-memory',
      title: 'Containers: memory',
      titles: {
        de: 'Container: Arbeitsspeicher',
        fr: 'Conteneurs : mémoire',
        es: 'Contenedores: memoria',
      },
      items: names.map((name) => ({ id: name, label: name, unit: 'bytes', history: true })),
    },
  ];
}

/**
 * What the processes add-on reports: the busiest processes by CPU and by memory, each as [pid, name], busiest
 * first. Their values come from values() as "extra:processes-cpu/pid-<pid>" and "extra:processes-memory/pid-<pid>".
 */
function processesAddOn(cpu: [number, string][], memory: [number, string][]): Extra[] {
  const items = (processes: [number, string][], unit: 'percent' | 'bytes') => {
    const seen: Record<string, number> = {};
    return processes.map(([pid, name]) => {
      seen[name] = (seen[name] ?? 0) + 1;
      const label = seen[name] > 1 ? `${name} (${seen[name]})` : name;
      return { id: `pid-${pid}`, label, unit, history: false };
    });
  };
  return [
    {
      id: 'processes-cpu',
      title: 'Top processes by CPU',
      titles: {
        de: 'Prozesse mit der meisten CPU-Last',
        fr: 'Processus les plus gourmands en CPU',
        es: 'Procesos con más uso de CPU',
      },
      items: items(cpu, 'percent'),
    },
    {
      id: 'processes-memory',
      title: 'Top processes by memory',
      titles: {
        de: 'Prozesse mit dem meisten Arbeitsspeicher',
        fr: 'Processus les plus gourmands en mémoire',
        es: 'Procesos con más uso de memoria',
      },
      items: items(memory, 'bytes'),
    },
  ];
}

/** The values of the processes add-on: each process's share of cpu, and its memory, keyed by pid. */
function processValues(
  cpu: number,
  shares: Record<number, number>,
  memory: Record<number, number>,
): Values {
  const values: Values = {};
  for (const [pid, share] of Object.entries(shares)) {
    values[`extra:processes-cpu/pid-${pid}`] = cpu * share;
  }
  for (const [pid, bytes] of Object.entries(memory)) {
    values[`extra:processes-memory/pid-${pid}`] = bytes;
  }
  return values;
}

const piHub: DemoMachine = {
  device: LOCAL_DEVICE,
  os: 'linux',
  cores: 4,
  maxClockMHz: 2400,
  memoryBytes: 8 * GB,
  swapBytes: 0.5 * GB,
  disks: [
    { path: '/', totalBytes: 117 * GB },
    { path: '/mnt/usb', totalBytes: 932 * GB },
  ],
  network: [{ name: 'eth0', addresses: ['192.168.1.10'], linkMbps: 1000 }, { name: 'wlan0' }],
  gpus: [{ name: 'VideoCore VII' }],
  fans: ['pwmfan'],
  throttling: { now: [], sinceBoot: ['softTemperatureLimit'] },
  extras: [
    ...powerAddOn([
      {
        id: 'raspberry-pi',
        label: 'Raspberry Pi (total)',
        labels: {
          de: 'Raspberry Pi (gesamt)',
          fr: 'Raspberry Pi (total)',
          es: 'Raspberry Pi (total)',
        },
      },
    ]),
    ...inodesAddOn([
      { id: 'root', label: '/' },
      { id: 'mnt-usb', label: '/mnt/usb' },
    ]),
    ...pressureAddOn(),
    ...kernelAddOn(),
    ...memoryAddOn(),
    ...portsAddOn([
      ['tcp', 22, '0.0.0.0, ::'],
      ['tcp', 9393, '0.0.0.0'],
      ['udp', 68, '0.0.0.0'],
      ['udp', 5353, '0.0.0.0, ::'],
    ]),
    ...processesAddOn(
      [
        [812, 'usage-control'],
        [655, 'dockerd'],
        [1432, 'pihole-FTL'],
        [610, 'containerd'],
        [1, 'systemd'],
        [433, 'systemd-journald'],
      ],
      [
        [655, 'dockerd'],
        [1432, 'pihole-FTL'],
        [812, 'usage-control'],
        [610, 'containerd'],
        [433, 'systemd-journald'],
        [1, 'systemd'],
      ],
    ),
  ],
  bootedDaysAgo: 12.3,
  values: (t, step) => {
    const cpu = vary(
      t,
      step,
      1,
      9,
      [
        [5, 40],
        [4, 600],
        [3, 7200],
      ],
      1,
    );
    return {
      cpu,
      memory: vary(t, step, 2, 38, [
        [3, 900],
        [2, 86400],
      ]),
      swap: vary(t, step, 3, 4, [[2, 86400]]),
      'temperature:cpu_thermal': 44 + cpu * 0.35 + vary(t, step, 4, 0, [[2, 300]], -5, 5),
      'disk:/': vary(t, step, 5, 21, [[0.3, 86400 * 5]]),
      'disk:/mnt/usb': vary(t, step, 6, 46, [[1, 86400 * 9]]),
      'disk.read:/': vary(t, step, 7, 40e3, [[35e3, 60]], 0, 1e9),
      'disk.write:/': vary(t, step, 8, 180e3, [[150e3, 30]], 0, 1e9),
      'disk.read:/mnt/usb': vary(t, step, 9, 5e3, [[5e3, 120]], 0, 1e9),
      'disk.write:/mnt/usb': vary(t, step, 10, 60e3, [[55e3, 90]], 0, 1e9),
      'network.receive:eth0': vary(
        t,
        step,
        11,
        90e3,
        [
          [60e3, 30],
          [30e3, 900],
        ],
        0,
        1e9,
      ),
      'network.send:eth0': vary(t, step, 12, 35e3, [[25e3, 45]], 0, 1e9),
      'gpu:VideoCore VII': vary(t, step, 13, 3, [[3, 120]]),
      'extra:power/raspberry-pi': 2.6 + cpu * 0.045,
      'extra:inodes/root': vary(t, step, 14, 9, [[0.2, 86400 * 5]]),
      'extra:inodes/mnt-usb': vary(t, step, 15, 3, [[0.1, 86400 * 9]]),
      'extra:pressure/cpu-some': Math.max(0, cpu * 0.12 - 0.3),
      'extra:pressure/memory-some': 0,
      'extra:pressure/memory-full': 0,
      // The SD card makes the Pi wait for I/O now and then.
      ...ioPressure(vary(t, step, 301, 1.5, [[2, 120]]), 0.5),
      ...kernelValues(t, step, 900, cpu, 1),
      ...memoryValues(t, step, 140, 8 * GB, cpu / 100),
      'extra:ports/count': 4,
      ...processValues(
        cpu,
        { 812: 0.3, 655: 0.18, 1432: 0.12, 610: 0.08, 1: 0.03, 433: 0.02 },
        { 655: 92e6, 1432: 61e6, 812: 38e6, 610: 34e6, 433: 21e6, 1: 12e6 },
      ),
    };
  },
};

const windowsPc: DemoMachine = {
  device: {
    id: 'windows-pc',
    kind: 'pc',
    name: 'Windows PC',
    address: '192.168.1.21:9393',
    removable: true,
  },
  os: 'windows',
  cores: 16,
  maxClockMHz: 4700,
  memoryBytes: 32 * GB,
  swapBytes: 4.75 * GB,
  disks: [
    { path: 'C:\\', totalBytes: 931 * GB },
    { path: 'D:\\', totalBytes: 1.82 * TB },
  ],
  network: [
    { name: 'Ethernet', addresses: ['192.168.1.21'], linkMbps: 2500 },
    { name: 'WLAN', addresses: ['192.168.1.121'] },
    { name: 'Bluetooth-Netzwerkverbindung' },
    { name: 'vEthernet (WSL)', addresses: ['172.23.80.1'] },
    { name: 'OpenVPN Data Channel Offload' },
  ],
  gpus: [{ name: 'NVIDIA GeForce RTX 4070', memoryBytes: 12 * GB }],
  extras: gpuAddOn('P2'),
  bootedDaysAgo: 0.4,
  values: (t, step) => {
    const busy = workday(t);
    const gpu = vary(t, step, 21, 8 + 35 * busy, [
      [25, 1800],
      [10, 60],
    ]);
    return {
      cpu: vary(
        t,
        step,
        22,
        6 + 22 * busy,
        [
          [12, 30],
          [8, 900],
        ],
        1,
      ),
      memory: vary(t, step, 23, 41 + 15 * busy, [[6, 1200]]),
      swap: vary(t, step, 24, 11, [[4, 7200]]),
      'disk:C:\\': vary(t, step, 25, 63, [[0.5, 86400 * 4]]),
      'disk:D:\\': vary(t, step, 26, 78, [[1, 86400 * 7]]),
      'disk.read:C:\\': vary(t, step, 27, 2e6 * busy + 300e3, [[1.5e6, 20]], 0, 2e9),
      'disk.write:C:\\': vary(t, step, 28, 1e6 * busy + 200e3, [[900e3, 25]], 0, 2e9),
      'disk.read:D:\\': vary(t, step, 29, 400e3 * busy, [[3e6, 300]], 0, 2e9),
      'disk.write:D:\\': vary(t, step, 30, 100e3, [[500e3, 400]], 0, 2e9),
      'network.receive:Ethernet': vary(
        t,
        step,
        31,
        600e3 + 3e6 * busy,
        [
          [2.5e6, 120],
          [500e3, 15],
        ],
        0,
        3e8,
      ),
      'network.send:Ethernet': vary(t, step, 32, 120e3 + 300e3 * busy, [[200e3, 60]], 0, 3e8),
      'network.receive:vEthernet (WSL)': vary(t, step, 33, 4e3, [[4e3, 300]], 0, 1e9),
      'network.send:vEthernet (WSL)': vary(t, step, 34, 2e3, [[2e3, 300]], 0, 1e9),
      'gpu:NVIDIA GeForce RTX 4070': gpu,
      'gpu.memory:NVIDIA GeForce RTX 4070': 18 + gpu * 0.5,
      ...gpuAddOnValues(gpu, 2475, 10501, 200, vary(t, step, 35, 4 * busy, [[6, 900]], 0, 100)),
    };
  },
};

/** The interface GUID of the Windows laptop's Wi-Fi adapter, which the Wi-Fi add-on keeps its values by. */
const WINDOWS_WIFI = '5c3e9a1f7b2d4e68a0c4d91f2b8e6a37';

const windowsLaptop: DemoMachine = {
  device: {
    id: 'windows-laptop',
    kind: 'pc',
    name: 'Windows laptop',
    address: '192.168.1.34:9393',
    removable: true,
  },
  os: 'windows',
  cores: 12,
  maxClockMHz: 3400,
  memoryBytes: 16 * GB,
  swapBytes: 2.5 * GB,
  disks: [{ path: 'C:\\', totalBytes: 476 * GB }],
  network: [
    { name: 'WLAN', addresses: ['192.168.1.34'] },
    { name: 'Bluetooth-Netzwerkverbindung' },
    { name: 'LAN-Verbindung* 1' },
  ],
  gpus: [{ name: 'Qualcomm Adreno X1-85 GPU' }],
  extras: wifiAddOn(
    'Qualcomm FastConnect 7800 Wi-Fi 7 High Band Simultaneous (HBS) Network Adapter',
    WINDOWS_WIFI,
  ),
  bootedDaysAgo: 2.1,
  values: (t, step) => ({
    cpu: vary(
      t,
      step,
      41,
      14,
      [
        [10, 20],
        [8, 600],
      ],
      1,
    ),
    memory: vary(t, step, 42, 58, [[7, 1500]]),
    swap: vary(t, step, 43, 9, [[3, 7200]]),
    battery: batteryPercent(t),
    'disk:C:\\': vary(t, step, 44, 52, [[0.5, 86400 * 3]]),
    'disk.read:C:\\': vary(t, step, 45, 700e3, [[600e3, 20]], 0, 2e9),
    'disk.write:C:\\': vary(t, step, 46, 400e3, [[350e3, 30]], 0, 2e9),
    'network.receive:WLAN': vary(
      t,
      step,
      47,
      400e3,
      [
        [350e3, 40],
        [200e3, 600],
      ],
      0,
      1e9,
    ),
    'network.send:WLAN': vary(t, step, 48, 60e3, [[50e3, 30]], 0, 1e9),
    'gpu:Qualcomm Adreno X1-85 GPU': vary(t, step, 49, 9, [[7, 90]]),
    ...wifiValues(
      WINDOWS_WIFI,
      vary(
        t,
        step,
        50,
        -62,
        [
          [5, 90],
          [6, 3600],
        ],
        -88,
        -40,
      ),
      'windows',
    ),
  }),
};

const windowsServer: DemoMachine = {
  device: {
    id: 'windows-server',
    kind: 'server',
    name: 'Windows Server',
    address: '192.168.1.5:9393',
  },
  os: 'windows',
  cores: 8,
  maxClockMHz: 3000,
  memoryBytes: 64 * GB,
  swapBytes: 8 * GB,
  disks: [
    { path: 'C:\\', totalBytes: 237 * GB },
    { path: 'E:\\', totalBytes: 7.28 * TB },
  ],
  network: [
    { name: 'Ethernet 1', addresses: ['192.168.1.5'], linkMbps: 10000 },
    { name: 'Ethernet 2', addresses: ['10.0.0.5'], linkMbps: 10000 },
    { name: 'Ethernet 3' },
    { name: 'Ethernet 4' },
  ],
  extras: windowsMemoryAddOn(),
  utc: true,
  bootedDaysAgo: 23.6,
  values: (t, step) => {
    const busy = workday(t);
    return {
      cpu: vary(
        t,
        step,
        51,
        12 + 18 * busy,
        [
          [8, 60],
          [6, 1200],
        ],
        1,
      ),
      memory: vary(t, step, 52, 71, [
        [4, 3600],
        [3, 86400],
      ]),
      swap: vary(t, step, 53, 6, [[2, 86400]]),
      'disk:C:\\': vary(t, step, 54, 48, [[0.5, 86400 * 6]]),
      'disk:E:\\': vary(t, step, 55, 64, [[2, 86400 * 10]]),
      'disk.read:C:\\': vary(t, step, 56, 300e3, [[250e3, 30]], 0, 2e9),
      'disk.write:C:\\': vary(t, step, 57, 600e3, [[400e3, 20]], 0, 2e9),
      'disk.read:E:\\': vary(t, step, 58, 4e6 + 20e6 * busy, [[8e6, 120]], 0, 2e9),
      'disk.write:E:\\': vary(t, step, 59, 1e6 + 6e6 * busy, [[3e6, 180]], 0, 2e9),
      'network.receive:Ethernet 1': vary(t, step, 60, 1.5e6 + 6e6 * busy, [[3e6, 60]], 0, 1e9),
      'network.send:Ethernet 1': vary(t, step, 61, 3e6 + 22e6 * busy, [[10e6, 90]], 0, 1e9),
      'network.receive:Ethernet 2': vary(t, step, 62, 800e3, [[600e3, 300]], 0, 1e9),
      'network.send:Ethernet 2': vary(t, step, 63, 300e3, [[250e3, 300]], 0, 1e9),
      ...windowsMemoryValues(t, step, 170, 64 * GB, busy),
    };
  },
};

const linuxDesktop: DemoMachine = {
  device: {
    id: 'linux-desktop',
    kind: 'pc',
    name: 'Linux desktop',
    address: '192.168.1.22:9393',
    removable: true,
  },
  os: 'linux',
  cores: 12,
  maxClockMHz: 5400,
  memoryBytes: 32 * GB,
  swapBytes: 8 * GB,
  disks: [
    { path: '/', totalBytes: 915 * GB },
    { path: '/home', totalBytes: 1.79 * TB },
  ],
  network: [
    { name: 'enp5s0', addresses: ['192.168.1.22'], linkMbps: 1000 },
    { name: 'wlp4s0' },
    { name: 'docker0', addresses: ['172.17.0.1'] },
  ],
  gpus: [{ name: 'AMD Radeon RX 7800 XT', memoryBytes: 16 * GB }],
  bootedDaysAgo: 1.2,
  values: (t, step) => {
    const busy = workday(t);
    const cpu = vary(
      t,
      step,
      71,
      5 + 25 * busy,
      [
        [15, 45],
        [8, 1200],
      ],
      1,
    );
    const gpu = vary(t, step, 72, 4 + 30 * busy, [
      [25, 2400],
      [8, 60],
    ]);
    return {
      cpu,
      memory: vary(t, step, 73, 34 + 20 * busy, [[5, 1800]]),
      swap: vary(t, step, 74, 2, [[2, 86400]]),
      'temperature:k10temp Tctl': 42 + cpu * 0.4 + vary(t, step, 75, 0, [[2, 120]], -5, 5),
      'temperature:amdgpu edge': 38 + gpu * 0.35,
      'temperature:nvme Composite': 38 + vary(t, step, 76, 0, [[4, 900]], -10, 10),
      'disk:/': vary(t, step, 77, 37, [[0.5, 86400 * 4]]),
      'disk:/home': vary(t, step, 78, 58, [[1, 86400 * 8]]),
      'disk.read:/': vary(t, step, 79, 500e3 + 3e6 * busy, [[2e6, 30]], 0, 3e9),
      'disk.write:/': vary(t, step, 80, 300e3 + 1e6 * busy, [[800e3, 40]], 0, 3e9),
      'disk.read:/home': vary(t, step, 81, 200e3, [[1e6, 200]], 0, 3e9),
      'disk.write:/home': vary(t, step, 82, 100e3, [[400e3, 300]], 0, 3e9),
      'network.receive:enp5s0': vary(t, step, 83, 300e3 + 2e6 * busy, [[1.5e6, 90]], 0, 1.2e8),
      'network.send:enp5s0': vary(t, step, 84, 80e3 + 200e3 * busy, [[150e3, 60]], 0, 1.2e8),
      'network.receive:docker0': vary(t, step, 85, 20e3, [[20e3, 120]], 0, 1e9),
      'network.send:docker0': vary(t, step, 86, 25e3, [[25e3, 120]], 0, 1e9),
      'gpu:AMD Radeon RX 7800 XT': gpu,
      'gpu.memory:AMD Radeon RX 7800 XT': 10 + gpu * 0.45,
    };
  },
};

const linuxNas: DemoMachine = {
  device: { id: 'linux-nas', kind: 'server', name: 'Linux NAS', address: '192.168.1.6:9393' },
  os: 'linux',
  cores: 4,
  maxClockMHz: 2900,
  memoryBytes: 16 * GB,
  disks: [
    { path: '/', totalBytes: 56 * GB },
    { path: '/srv/data', totalBytes: 21.8 * TB },
    { path: '/srv/backup', totalBytes: 10.9 * TB },
  ],
  network: [
    { name: 'enp1s0', addresses: ['192.168.1.6'], linkMbps: 2500 },
    { name: 'enp2s0', linkMbps: 2500 },
  ],
  fans: ['nct6798 fan1', 'nct6798 fan2'],
  extras: [
    ...pressureAddOn(),
    ...containersAddOn(['jellyfin', 'nextcloud', 'restic-backup']),
    ...smartAddOn([
      {
        id: 'wd-x1g2h3jk',
        name: 'WDC WD120EFBX-68B0EN0 (sda)',
        values: ['temperature', 'power-on-hours', 'reallocated'],
      },
      {
        id: 'zrt0abcd',
        name: 'ST12000VN0008-2YS101 (sdb)',
        values: ['temperature', 'power-on-hours', 'reallocated'],
      },
    ]),
  ],
  utc: true,
  bootedDaysAgo: 41.8,
  values: (t, step) => {
    // The backup runs at night and writes for a few hours.
    const backup = Math.max(0, 1 - workday(t) * 3) * Math.max(0, drift(t, 7200, 99));
    const drives = {
      sda: 34 + 4 * backup + vary(t, step, 93, 0, [[1.5, 1800]], -5, 5),
      sdb: 35 + 4 * backup + vary(t, step, 94, 0, [[1.5, 1800]], -5, 5),
    };
    const cpu = vary(
      t,
      step,
      91,
      4 + 30 * backup,
      [
        [3, 60],
        [3, 900],
      ],
      1,
    );
    return {
      cpu,
      memory: vary(t, step, 92, 27, [[3, 3600]]),
      'extra:pressure/cpu-some': Math.max(0, cpu * 0.15 - 0.4),
      'extra:pressure/memory-some': vary(t, step, 303, 0, [[0.4, 600]]),
      'extra:pressure/memory-full': 0,
      // The backup keeps the disks busy, so tasks wait for them.
      ...ioPressure(vary(t, step, 304, 0.5 + 35 * backup, [[1, 300]]), 0.55),
      'temperature:coretemp Package id 0': 39 + cpu * 0.3,
      'temperature:drivetemp sda': drives.sda,
      'temperature:drivetemp sdb': drives.sdb,
      // The smart add-on reads the disks every 30 minutes.
      'extra:smart/wd-x1g2h3jk-temperature': Math.round(drives.sda),
      'extra:smart/wd-x1g2h3jk-power-on-hours': Math.floor((t - openedAt) / 3600) + 21_408,
      'extra:smart/wd-x1g2h3jk-reallocated': 0,
      'extra:smart/zrt0abcd-temperature': Math.round(drives.sdb),
      'extra:smart/zrt0abcd-power-on-hours': Math.floor((t - openedAt) / 3600) + 21_395,
      'extra:smart/zrt0abcd-reallocated': t < openedAt - 9 * 86400 ? 0 : 8,
      'disk:/': vary(t, step, 95, 31, [[0.3, 86400 * 5]]),
      'disk:/srv/data': vary(t, step, 96, 71, [[1.5, 86400 * 12]]),
      'disk:/srv/backup': vary(t, step, 97, 83, [[2, 86400 * 6]]),
      'disk.read:/': vary(t, step, 98, 20e3, [[20e3, 60]], 0, 1e9),
      'disk.write:/': vary(t, step, 99, 60e3, [[50e3, 30]], 0, 1e9),
      'disk.read:/srv/data': vary(t, step, 100, 3e6 + 60e6 * backup, [[4e6, 120]], 0, 1e9),
      'disk.write:/srv/data': vary(t, step, 101, 500e3, [[1e6, 300]], 0, 1e9),
      'disk.read:/srv/backup': vary(t, step, 102, 100e3, [[200e3, 300]], 0, 1e9),
      'disk.write:/srv/backup': vary(t, step, 103, 200e3 + 60e6 * backup, [[300e3, 120]], 0, 1e9),
      'network.receive:enp1s0': vary(
        t,
        step,
        104,
        800e3,
        [
          [600e3, 60],
          [400e3, 1800],
        ],
        0,
        3e8,
      ),
      'network.send:enp1s0': vary(
        t,
        step,
        105,
        3e6,
        [
          [3e6, 120],
          [2e6, 1800],
        ],
        0,
        3e8,
      ),
      'extra:containers-cpu/jellyfin': vary(t, step, 106, 1.5, [[1.5, 300]]),
      'extra:containers-cpu/nextcloud': vary(t, step, 107, 0.8, [[0.6, 120]]),
      'extra:containers-cpu/restic-backup': 0.1 + 20 * backup,
      'extra:containers-memory/jellyfin': vary(
        t,
        step,
        108,
        1.1 * GB,
        [[0.2 * GB, 3600]],
        0,
        GB * 4,
      ),
      'extra:containers-memory/nextcloud': vary(
        t,
        step,
        109,
        0.6 * GB,
        [[0.1 * GB, 1800]],
        0,
        GB * 4,
      ),
      'extra:containers-memory/restic-backup': 30e6 + 0.5 * GB * backup,
    };
  },
};

const linuxServer: DemoMachine = {
  device: {
    id: 'linux-server',
    kind: 'server',
    name: 'Linux build server',
    address: '192.168.1.7:9393',
  },
  os: 'linux',
  cores: 32,
  maxClockMHz: 4200,
  memoryBytes: 128 * GB,
  swapBytes: 16 * GB,
  disks: [
    { path: '/', totalBytes: 1.79 * TB },
    { path: '/var/lib/docker', totalBytes: 3.58 * TB },
  ],
  network: [
    { name: 'eno1', addresses: ['192.168.1.7'], linkMbps: 10000 },
    { name: 'eno2' },
    { name: 'docker0', addresses: ['172.17.0.1'] },
  ],
  gpus: [{ name: 'NVIDIA GeForce RTX 3090', memoryBytes: 24 * GB }],
  fans: ['nct6799 fan1', 'nct6799 fan2', 'nct6799 fan3'],
  extras: [
    ...powerAddOn([
      { id: 'rapl-0-package-0', label: 'CPU package 0', labels: cpuPackageLabels },
      { id: 'rapl-0-2-dram', label: 'Memory', labels: memoryLabels },
      { id: 'nvidia-0', label: 'NVIDIA GeForce RTX 3090' },
    ]),
    ...gpuAddOn('P2'),
    ...inodesAddOn([
      { id: 'root', label: '/' },
      { id: 'var-lib-docker', label: '/var/lib/docker' },
    ]),
    ...kernelAddOn(),
    ...memoryAddOn(),
    ...portsAddOn([
      ['tcp', 22, '0.0.0.0, ::'],
      ['tcp', 53, '127.0.0.53, 127.0.0.54'],
      ['tcp', 443, '0.0.0.0, ::'],
      ['tcp', 5000, '0.0.0.0, ::'],
      ['tcp', 9100, '::'],
      ['tcp', 9393, '0.0.0.0, ::'],
      ['udp', 53, '127.0.0.53, 127.0.0.54'],
      ['udp', 5353, '0.0.0.0, ::'],
    ]),
    ...containersAddOn(['gitea-runner', 'registry']),
    ...smartAddOn([
      {
        id: 's69enx0t123456a',
        name: 'Samsung SSD 980 PRO 2TB (nvme0)',
        values: ['temperature', 'power-on-hours', 'media-errors', 'used'],
      },
    ]),
    ...processesAddOn(
      [
        [48213, 'cc1plus'],
        [48207, 'cc1plus'],
        [48190, 'cc1plus'],
        [47950, 'ld'],
        [2210, 'dockerd'],
        [3304, 'java'],
      ],
      [
        [3304, 'java'],
        [5120, 'postgres'],
        [2210, 'dockerd'],
        [48213, 'cc1plus'],
        [48207, 'cc1plus'],
        [1890, 'containerd'],
      ],
    ),
  ],
  utc: true,
  bootedDaysAgo: 87.4,
  values: (t, step) => {
    // Builds come in bursts of a few minutes.
    const build = Math.max(0, drift(t, 240, 111)) ** 0.7;
    const cpu = vary(t, step, 112, 6 + 80 * build, [[5, 20]], 1);
    const gpu = vary(t, step, 113, 10, [
      [40, 3600],
      [10, 120],
    ]);
    return {
      cpu,
      memory: vary(t, step, 114, 30 + 35 * build, [[4, 600]]),
      swap: vary(t, step, 115, 3, [[2, 86400]]),
      'temperature:k10temp Tctl': 45 + cpu * 0.38,
      'temperature:NVIDIA GeForce RTX 3090': 36 + gpu * 0.4,
      'temperature:nvme Composite': 41 + 8 * build,
      'disk:/': vary(t, step, 116, 44, [[0.5, 86400 * 3]]),
      'disk:/var/lib/docker': vary(t, step, 117, 61, [[4, 86400 * 2]]),
      'disk.read:/': vary(t, step, 118, 1e6 + 40e6 * build, [[2e6, 15]], 0, 4e9),
      'disk.write:/': vary(t, step, 119, 500e3 + 25e6 * build, [[1e6, 15]], 0, 4e9),
      'disk.read:/var/lib/docker': vary(t, step, 120, 300e3 + 15e6 * build, [[1e6, 30]], 0, 4e9),
      'disk.write:/var/lib/docker': vary(t, step, 121, 200e3 + 30e6 * build, [[1e6, 30]], 0, 4e9),
      'network.receive:eno1': vary(t, step, 122, 1e6 + 40e6 * build, [[2e6, 30]], 0, 1.2e9),
      'network.send:eno1': vary(t, step, 123, 500e3 + 8e6 * build, [[1e6, 30]], 0, 1.2e9),
      'network.receive:docker0': vary(t, step, 124, 50e3 + 3e6 * build, [[100e3, 60]], 0, 1e9),
      'network.send:docker0': vary(t, step, 125, 80e3 + 5e6 * build, [[100e3, 60]], 0, 1e9),
      'gpu:NVIDIA GeForce RTX 3090': gpu,
      'gpu.memory:NVIDIA GeForce RTX 3090': 6 + gpu * 0.6,
      'extra:power/rapl-0-package-0': 18 + cpu * 1.4,
      'extra:power/rapl-0-2-dram': 6 + 4 * build,
      'extra:power/nvidia-0': 32 + gpu * 3.1,
      ...gpuAddOnValues(gpu, 1695, 9751, 350, 0),
      'extra:inodes/root': vary(t, step, 126, 6, [[0.3, 86400 * 3]]),
      'extra:inodes/var-lib-docker': vary(t, step, 127, 38 + 2 * build, [[5, 86400 * 2]]),
      ...kernelValues(t, step, 950, cpu, 8),
      ...memoryValues(t, step, 160, 128 * GB, build),
      'extra:ports/count': 8,
      // The runner does the builds.
      'extra:containers-cpu/gitea-runner': 0.2 + 70 * build,
      'extra:containers-cpu/registry': vary(t, step, 128, 0.3, [[0.3, 60]]) + 2 * build,
      'extra:containers-memory/gitea-runner': 0.3 * GB + 30 * GB * build,
      'extra:containers-memory/registry': vary(t, step, 129, 0.15 * GB, [[0.05 * GB, 3600]], 0, GB),
      'extra:smart/s69enx0t123456a-temperature': Math.round(41 + 8 * build),
      'extra:smart/s69enx0t123456a-power-on-hours': Math.floor((t - openedAt) / 3600) + 9_874,
      'extra:smart/s69enx0t123456a-media-errors': 0,
      'extra:smart/s69enx0t123456a-used': 4,
      ...processValues(
        cpu,
        { 48213: 0.22, 48207: 0.2, 48190: 0.17, 47950: 0.08, 2210: 0.03, 3304: 0.02 },
        {
          3304: 8.2 * GB,
          5120: 3.1 * GB,
          2210: 0.9 * GB + 0.4 * GB * build,
          48213: 0.15 * GB + 0.6 * GB * build,
          48207: 0.15 * GB + 0.5 * GB * build,
          1890: 0.2 * GB,
        },
      ),
    };
  },
};

/** The laptop sleeps at night, and went to sleep shortly before the page was opened. */
const laptopOnline = (t: number) => t < openedAt - 47 * 60 && hourOf(t) >= 6;

/** When a device that does not answer now stopped answering, to five minutes. */
export function offlineSince(online: (t: number) => boolean, now: number): number {
  let t = Math.floor(now / 300) * 300;
  while (!online(t - 300) && t > now - 7 * 86400) {
    t -= 300;
  }
  return t;
}

/**
 * The Wi-Fi add-on's values of the interface id at a signal level, with the quality the system derives from it: Linux's
 * cfg80211 link quality, or on Windows its signal quality, 0 % at -100 dBm and 100 % at -50 dBm.
 */
function wifiValues(id: string, signal: number, os: 'linux' | 'windows' = 'linux'): Values {
  const dBm = Math.round(signal);
  const quality =
    os === 'windows'
      ? Math.min(100, Math.max(0, (dBm + 100) * 2))
      : (Math.min(70, Math.max(0, dBm + 110)) / 70) * 100;
  return { [`extra:wifi/${id}-signal`]: dBm, [`extra:wifi/${id}-quality`]: quality };
}

const linuxLaptop: DemoMachine = {
  device: {
    id: 'linux-laptop',
    kind: 'pc',
    name: 'Linux laptop',
    address: '192.168.1.38:9393',
    removable: true,
    unreachable: true,
    unreachableSince: new Date(offlineSince(laptopOnline, openedAt) * 1000).toISOString(),
  },
  os: 'linux',
  cores: 8,
  maxClockMHz: 4400,
  memoryBytes: 16 * GB,
  swapBytes: 16 * GB,
  disks: [{ path: '/', totalBytes: 476 * GB }],
  network: [{ name: 'wlp1s0', addresses: ['192.168.1.38'] }],
  extras: wifiAddOn('wlp1s0'),
  batteryDetails: true,
  bootedDaysAgo: 0.3,
  online: laptopOnline,
  values: (t, step) => {
    const cpu = vary(
      t,
      step,
      131,
      12,
      [
        [9, 30],
        [6, 900],
      ],
      1,
    );
    return {
      cpu,
      memory: vary(t, step, 132, 46, [[6, 1200]]),
      swap: vary(t, step, 133, 1, [[1, 86400]]),
      battery: batteryPercent(t + 4000),
      'temperature:coretemp Package id 0': 46 + cpu * 0.45,
      'disk:/': vary(t, step, 134, 68, [[0.5, 86400 * 4]]),
      'disk.read:/': vary(t, step, 135, 400e3, [[350e3, 25]], 0, 2e9),
      'disk.write:/': vary(t, step, 136, 250e3, [[200e3, 25]], 0, 2e9),
      'network.receive:wlp1s0': vary(t, step, 137, 300e3, [[250e3, 60]], 0, 1e9),
      'network.send:wlp1s0': vary(t, step, 138, 50e3, [[40e3, 60]], 0, 1e9),
      ...wifiValues(
        'wlp1s0',
        vary(
          t,
          step,
          139,
          -58,
          [
            [6, 120],
            [5, 3600],
          ],
          -85,
          -35,
        ),
      ),
    };
  },
};

export const FLEET: readonly DemoMachine[] = [
  piHub,
  windowsPc,
  windowsLaptop,
  windowsServer,
  linuxDesktop,
  linuxNas,
  linuxServer,
  linuxLaptop,
];

/** The values whose names start with kind and a colon, by what follows the colon. */
function named(values: Values, kind: string): [string, number][] {
  return Object.entries(values)
    .filter(([metric]) => metric.startsWith(kind + ':'))
    .map(([metric, value]) => [metric.slice(kind.length + 1), value]);
}

/** The visitor's time zone, or UTC for servers, as the backend reports it. */
function timeZone(machine: DemoMachine, t: number): TimeZone {
  if (machine.utc) {
    return { name: 'UTC', offsetSeconds: 0 };
  }
  const date = new Date(t * 1000);
  const offsetSeconds = -date.getTimezoneOffset() * 60;
  const name = new Intl.DateTimeFormat('en-US', { timeZoneName: 'short' })
    .formatToParts(date)
    .find((part) => part.type === 'timeZoneName')?.value;
  // Zones without a short name of their own are named by their offset, like Linux does.
  const hours = Math.trunc(Math.abs(offsetSeconds) / 3600);
  const offsetName = `${offsetSeconds < 0 ? '-' : '+'}${String(hours).padStart(2, '0')}`;
  return { name: name && /^[A-Z]{2,5}$/.test(name) ? name : offsetName, offsetSeconds };
}

/** What GET /api/metrics answers for the machine at t, in Unix seconds. */
export function snapshotOf(machine: DemoMachine, t: number): Snapshot {
  const v = machine.values(t, 0);
  const linux = machine.os === 'linux';
  const uptimeSeconds = Math.round(machine.bootedDaysAgo * 86400 + (t - openedAt));
  const seed = machine.device.id.length * 17;
  const percentOf = (total: number, percent: number) => Math.round((total * percent) / 100);
  // The load average over a window: the usage at a few moments in it, as busy cores.
  const load = (window: number) => {
    const moments = [0, 1, 2, 3, 4, 5].map((i) => machine.values(t - (i * window) / 6, 0)['cpu']);
    return (moments.reduce((sum, cpu) => sum + cpu, 0) / moments.length / 100) * machine.cores;
  };
  const disks = machine.disks.map((disk) => {
    const read = v[`disk.read:${disk.path}`] ?? 0;
    const write = v[`disk.write:${disk.path}`] ?? 0;
    const busyPercent = Math.min(100, (read + write) / 2e6);
    return {
      path: disk.path,
      totalBytes: Math.round(disk.totalBytes),
      usedBytes: percentOf(disk.totalBytes, v[`disk:${disk.path}`]),
      usedPercent: v[`disk:${disk.path}`],
      readBytesPerSecond: read,
      writeBytesPerSecond: write,
      operationsPerSecond: (read + write) / (64 * 1024) + 2,
      ...(linux ? { busyPercent, latencyMs: 0.4 + busyPercent / 40 } : {}),
    };
  });
  const memoryUsed = percentOf(machine.memoryBytes, v['memory']);
  const temperatures = named(v, 'temperature').map(([sensor, celsius]) => ({ sensor, celsius }));
  const hottest = Math.max(45, ...temperatures.map((temp) => temp.celsius));

  return {
    time: new Date(t * 1000).toISOString(),
    timeZone: timeZone(machine, t),
    uptimeSeconds,
    cpu: {
      usagePercent: v['cpu'],
      cores: machine.cores,
      coreUsagePercent: Array.from({ length: machine.cores }, (_, core) =>
        vary(t, 0, seed + core, v['cpu'], [[v['cpu'] * 0.8 + 4, 6]], 0, 100),
      ),
      clockMHz: Math.round(
        machine.maxClockMHz * (0.45 + (0.55 * Math.min(100, v['cpu'] * 3)) / 100),
      ),
      ...(linux
        ? {
            loadAverage: { one: load(60), five: load(300), fifteen: load(900) },
            ioWaitPercent: Math.min(
              20,
              disks.reduce((sum, d) => sum + (d.busyPercent ?? 0), 0) / 20,
            ),
            stealPercent: 0,
            processes: {
              total: Math.round(140 + machine.cores * 12 + v['memory']),
              running: Math.max(1, Math.round((v['cpu'] / 100) * machine.cores)),
            },
          }
        : {}),
    },
    memory: {
      totalBytes: machine.memoryBytes,
      usedBytes: memoryUsed,
      usedPercent: v['memory'],
      availableBytes: machine.memoryBytes - memoryUsed,
      ...(linux ? { cachedBytes: Math.round((machine.memoryBytes - memoryUsed) * 0.6) } : {}),
      ...(machine.swapBytes
        ? {
            swap: {
              totalBytes: machine.swapBytes,
              usedBytes: percentOf(machine.swapBytes, v['swap']),
              usedPercent: v['swap'],
            },
          }
        : {}),
    },
    temperatures,
    disks,
    network: machine.network.map((n) => {
      const receive = v[`network.receive:${n.name}`] ?? 0;
      const send = v[`network.send:${n.name}`] ?? 0;
      return {
        name: n.name,
        // A rough total: the current speed over the time since the machine started.
        receivedBytes: Math.round(receive * uptimeSeconds * 0.8),
        sentBytes: Math.round(send * uptimeSeconds * 0.8),
        receiveBytesPerSecond: receive,
        sendBytesPerSecond: send,
        ...(n.linkMbps ? { linkMbps: n.linkMbps } : {}),
        ...(n.addresses ? { addresses: n.addresses } : {}),
      };
    }),
    gpus: (machine.gpus ?? []).map((gpu) => {
      const usage = v[`gpu:${gpu.name}`];
      return {
        name: gpu.name,
        usagePercent: usage,
        ...(gpu.memoryBytes
          ? {
              memoryTotalBytes: gpu.memoryBytes,
              memoryUsedBytes: percentOf(gpu.memoryBytes, v[`gpu.memory:${gpu.name}`]),
            }
          : {}),
        // Windows reports no GPU temperature.
        ...(linux && gpu.memoryBytes ? { celsius: 36 + usage * 0.4 } : {}),
      };
    }),
    ...(machine.throttling ? { throttling: machine.throttling } : {}),
    ...(v['battery'] !== undefined
      ? {
          battery: {
            percent: v['battery'],
            pluggedIn: charging(t),
            ...(machine.batteryDetails
              ? { watts: charging(t) ? 38 : 7 + v['cpu'] * 0.2, healthPercent: 91 }
              : {}),
          },
        }
      : {}),
    ...(machine.fans
      ? {
          fans: machine.fans.map((name, i) => ({
            name,
            rpm: Math.round(800 + (hottest - 35) * 60 + i * 140),
          })),
        }
      : {}),
    ...(machine.extras
      ? {
          extras: machine.extras.map((group) => ({
            ...group,
            items: group.items.map((item) =>
              item.unit === 'text' ? item : { ...item, value: v[`extra:${group.id}/${item.id}`] },
            ),
          })),
        }
      : {}),
  };
}

/** How the machine's extras that keep their history are described, by metric, as GET /api/history says. */
export function extraInfoOf(machine: DemoMachine): Record<string, ExtraInfo> {
  const info: Record<string, ExtraInfo> = {};
  for (const group of machine.extras ?? []) {
    for (const item of group.items.filter((i) => i.history)) {
      info[`extra:${group.id}/${item.id}`] = {
        title: group.title,
        titles: group.titles,
        label: item.label,
        labels: item.labels,
        unit: item.unit,
      };
    }
  }
  return info;
}
