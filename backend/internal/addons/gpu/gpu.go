// Package gpu reads more about the NVIDIA GPUs than usage-control itself
// does, for the gpu add-on: the fan, the graphics and memory clocks, the
// video encoder and decoder, the performance state and the power limit, all
// from one call of nvidia-smi, which comes with the NVIDIA driver. Usage,
// memory and temperature are on usage-control's own page, and the power the
// GPU draws comes from the power add-on.
//
// It only reads; nothing in here changes the machine.
package gpu

import (
	"context"
	"log/slog"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

const (
	// query is what nvidia-smi is asked, in this order, one line per GPU.
	query = "--query-gpu=index,name,fan.speed,clocks.gr,clocks.mem,utilization.encoder,utilization.decoder,pstate,power.limit"
	// nvidiaTimeout is how long nvidia-smi may take, as long as the power
	// add-on lets it. Without the driver's persistence mode, as on many
	// Linux servers, each call starts the driver, which can take more than
	// a second.
	nvidiaTimeout = 3 * time.Second
)

// fields are the values of query after the index and the name.
var fields = []struct {
	id      string
	label   string
	labels  map[string]string
	unit    metrics.Unit
	history bool
}{
	{"fan", "Fan", map[string]string{"de": "Lüfter", "fr": "Ventilateur", "es": "Ventilador"}, metrics.UnitPercent, true},
	{"graphics-clock", "Graphics clock (MHz)", map[string]string{"de": "Grafiktakt (MHz)", "fr": "Fréquence graphique (MHz)", "es": "Frecuencia gráfica (MHz)"}, metrics.UnitNumber, true},
	{"memory-clock", "Memory clock (MHz)", map[string]string{"de": "Speichertakt (MHz)", "fr": "Fréquence mémoire (MHz)", "es": "Frecuencia de memoria (MHz)"}, metrics.UnitNumber, true},
	{"encoder", "Video encoder", map[string]string{"de": "Video-Encoder", "fr": "Encodeur vidéo", "es": "Codificador de vídeo"}, metrics.UnitPercent, true},
	{"decoder", "Video decoder", map[string]string{"de": "Video-Decoder", "fr": "Décodeur vidéo", "es": "Decodificador de vídeo"}, metrics.UnitPercent, true},
	{"performance-state", "Performance state", map[string]string{"de": "Leistungszustand", "fr": "État de performance", "es": "Estado de rendimiento"}, metrics.UnitText, false},
	{"power-limit", "Power limit", map[string]string{"de": "Leistungsgrenze", "fr": "Limite de puissance", "es": "Límite de potencia"}, metrics.UnitWatts, false},
}

// Reader reads the NVIDIA GPUs through nvidia-smi.
type Reader struct {
	// program is where nvidia-smi is; empty when it is not installed.
	program string
	// failing is whether the last call failed, so a failure is logged once.
	failing bool
}

// NewReader finds nvidia-smi on the PATH. Without it, which is the usual
// case without an NVIDIA GPU, the add-on reports nothing; that is logged
// once here instead of at every read.
func NewReader() *Reader {
	program, err := exec.LookPath("nvidia-smi")
	if err != nil {
		slog.Info("nvidia-smi is not installed, so there is no NVIDIA GPU to read")
		return &Reader{}
	}
	return &Reader{program: program}
}

// Read returns the GPUs' values as the group of extras the collector shows,
// or nothing when nvidia-smi is missing or prints nothing. It gives up after
// nvidiaTimeout, so a hanging driver does not hold up the next report.
func (r *Reader) Read(ctx context.Context, _ time.Time) []metrics.Extra {
	if r.program == "" {
		return nil
	}
	out, err := run(ctx, r.program)
	switch {
	case err != nil && !r.failing:
		slog.Warn("nvidia-smi failed", "error", err)
	case err == nil && r.failing:
		slog.Info("nvidia-smi works again")
	}
	r.failing = err != nil
	// When one GPU is in an error state, nvidia-smi still prints the others
	// but exits with an error, so what it printed is read either way.
	return Extras(Parse(out))
}

func run(ctx context.Context, program string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, nvidiaTimeout)
	defer cancel()
	// program is the nvidia-smi found on the PATH at start, and the arguments are fixed.
	cmd := exec.CommandContext(ctx, program, query, "--format=csv,noheader,nounits")
	metrics.HideWindow(cmd)
	out, err := cmd.Output()
	return string(out), err
}

// GPU is what nvidia-smi reports of one GPU. A value the GPU does not
// report, written as [N/A] or [Not Supported], is missing from Values.
type GPU struct {
	Index string
	Name  string
	// Values holds the fields' values by their id: numbers for all but the
	// performance state, which stays text.
	Values map[string]string
}

// Parse reads the CSV nvidia-smi writes for query, such as
// "0, NVIDIA GeForce RTX 3090, 30, 1695, 9751, 0, 0, P2, 350.00". Other
// lines, such as the message nvidia-smi prints for a GPU in an error state,
// are left out.
func Parse(out string) []GPU {
	var gpus []GPU
	for line := range strings.Lines(out) {
		parts := strings.Split(line, ",")
		if len(parts) < 2+len(fields) {
			continue
		}
		// The name is the only field that could hold a comma.
		values := parts[len(parts)-len(fields):]
		gpu := GPU{
			Index:  strings.TrimSpace(parts[0]),
			Name:   strings.TrimSpace(strings.Join(parts[1:len(parts)-len(fields)], ",")),
			Values: map[string]string{},
		}
		if _, err := strconv.Atoi(gpu.Index); err != nil {
			continue
		}
		for i, field := range fields {
			value := strings.TrimSpace(values[i])
			if value == "" || strings.HasPrefix(value, "[") {
				continue
			}
			if field.unit != metrics.UnitText {
				if _, err := strconv.ParseFloat(value, 64); err != nil {
					continue
				}
			}
			gpu.Values[field.id] = value
		}
		gpus = append(gpus, gpu)
	}
	return gpus
}

// Extras returns the GPUs as the group of extras the collector shows. With
// more than one GPU, each label starts with the GPU's name and index, so two
// identical cards can be told apart.
func Extras(gpus []GPU) []metrics.Extra {
	group := metrics.Extra{
		ID:     "gpu",
		Title:  "Graphics card",
		Titles: map[string]string{"de": "Grafikkarte", "fr": "Carte graphique", "es": "Tarjeta gráfica"},
	}
	for _, gpu := range gpus {
		prefix := ""
		if len(gpus) > 1 {
			prefix = gpu.Name + " (" + gpu.Index + "): "
		}
		for _, field := range fields {
			value, ok := gpu.Values[field.id]
			if !ok {
				continue
			}
			item := metrics.ExtraItem{
				ID:      idOf(gpu.Index, field.id),
				Label:   prefix + field.label,
				Labels:  map[string]string{},
				Unit:    field.unit,
				History: field.history,
			}
			for language, label := range field.labels {
				item.Labels[language] = prefix + label
			}
			if field.unit == metrics.UnitText {
				item.Text = value
			} else {
				number, _ := strconv.ParseFloat(value, 64)
				item.Value = &number
			}
			group.Items = append(group.Items, item)
		}
	}
	if len(group.Items) == 0 {
		return nil
	}
	return []metrics.Extra{group}
}

var notInID = regexp.MustCompile(`[^a-z0-9]+`)

// idOf turns parts of a name into an id for an extra: lowercase letters and
// digits joined by "-", at most 40 characters.
func idOf(parts ...string) string {
	id := strings.Trim(notInID.ReplaceAllString(strings.ToLower(strings.Join(parts, "-")), "-"), "-")
	if len(id) > 40 {
		id = strings.TrimRight(id[:40], "-")
	}
	return id
}
