package media

import (
	"bytes"
	"encoding/binary"
	"image/color"
	"os"
	"strconv"
	"testing"
)

func box(typ string, payload ...[]byte) []byte {
	n := 8
	for _, p := range payload {
		n += len(p)
	}
	b := binary.BigEndian.AppendUint32(nil, uint32(n))
	b = append(b, typ...)
	for _, p := range payload {
		b = append(b, p...)
	}
	return b
}

func fullBox(typ string, version byte, flags uint32, payload ...[]byte) []byte {
	vf := binary.BigEndian.AppendUint32(nil, uint32(version)<<24|flags)
	return box(typ, append([][]byte{vf}, payload...)...)
}

func u16(v int) []byte { return binary.BigEndian.AppendUint16(nil, uint16(v)) }
func u32(v int) []byte { return binary.BigEndian.AppendUint32(nil, uint32(v)) }

// findBox returns the payload of the box at path, descending into the
// sample description and visual sample entry layouts where needed.
func findBox(t *testing.T, b []byte, path ...string) []byte {
	t.Helper()
	for off := 0; off+8 <= len(b); {
		n := int(binary.BigEndian.Uint32(b[off:]))
		if n < 8 || off+n > len(b) {
			break
		}
		if string(b[off+4:off+8]) == path[0] {
			p := b[off+8 : off+n]
			if len(path) == 1 {
				return p
			}
			switch path[0] {
			case "stsd":
				p = p[8:] // version/flags, entry count
			case "hvc1":
				p = p[78:] // VisualSampleEntry fields
			}
			return findBox(t, p, path[1:]...)
		}
		off += n
	}
	t.Fatalf("box %v not found", path)
	return nil
}

// hevcFrame encodes one w×h frame and returns its hvcC box and sample.
func hevcFrame(t *testing.T, c string, w, h int) (hvcC, sample []byte) {
	t.Helper()
	size := strconv.Itoa(w) + "x" + strconv.Itoa(h)
	p := gen(t, "f.mp4", cat(lavfi("color=c="+c+":s="+size), "-frames:v", "1",
		"-c:v", "libx265", "-x265-params", "log-level=error", "-tag:v", "hvc1", "-pix_fmt", "yuv420p")...)
	mp4, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	cfg := findBox(t, mp4, "moov", "trak", "mdia", "minf", "stbl", "stsd", "hvc1", "hvcC")
	return box("hvcC", cfg), findBox(t, mp4, "mdat")
}

type heifItem struct {
	id     int
	typ    string
	hidden bool
	props  []int // 1-based ipco indexes, all marked essential
	data   []byte
}

// buildHEIF writes a minimal HEIF: ftyp, meta (hdlr, pitm, iinf, iref,
// iloc, iprp) and mdat holding every item's data.
func buildHEIF(primary int, items []heifItem, props [][]byte, dimg []int) []byte {
	ftyp := box("ftyp", []byte("heic"), u32(0), []byte("mif1heic"))
	build := func(base int) []byte {
		var infe [][]byte
		for _, it := range items {
			flags := uint32(0)
			if it.hidden {
				flags = 1
			}
			infe = append(infe, fullBox("infe", 2, flags, u16(it.id), u16(0), []byte(it.typ), []byte{0}))
		}
		iinf := fullBox("iinf", 0, 0, append([][]byte{u16(len(items))}, infe...)...)
		var iloc [][]byte
		iloc = append(iloc, []byte{0x44, 0x00}, u16(len(items)))
		off := base
		for _, it := range items {
			iloc = append(iloc, u16(it.id), u16(0), u16(1), u32(off), u32(len(it.data)))
			off += len(it.data)
		}
		var ipma [][]byte
		ipma = append(ipma, u32(len(items)))
		for _, it := range items {
			ipma = append(ipma, u16(it.id), []byte{byte(len(it.props))})
			for _, p := range it.props {
				ipma = append(ipma, []byte{0x80 | byte(p)})
			}
		}
		parts := [][]byte{
			fullBox("hdlr", 0, 0, u32(0), []byte("pict"), make([]byte, 12), []byte{0}),
			fullBox("pitm", 0, 0, u16(primary)),
			iinf,
		}
		if dimg != nil {
			ref := [][]byte{u16(primary), u16(len(dimg))}
			for _, id := range dimg {
				ref = append(ref, u16(id))
			}
			parts = append(parts, fullBox("iref", 0, 0, box("dimg", ref...)))
		}
		parts = append(parts,
			fullBox("iloc", 0, 0, iloc...),
			box("iprp", box("ipco", props...), fullBox("ipma", 0, 0, ipma...)),
		)
		return fullBox("meta", 0, 0, parts...)
	}
	// The data offsets depend on the meta size, which does not depend on
	// the offsets' values.
	meta := build(0)
	meta = build(len(ftyp) + len(meta) + 8)
	var data [][]byte
	for _, it := range items {
		data = append(data, it.data)
	}
	return bytes.Join([][]byte{ftyp, meta, box("mdat", data...)}, nil)
}

func ispe(w, h int) []byte { return fullBox("ispe", 0, 0, u32(w), u32(h)) }

func TestHEIC(t *testing.T) {
	t.Parallel()
	r := newTestRunner(t, Config{})

	cfg, green := hevcFrame(t, "green", 128, 96)
	single := putFile(t, "single.heic", buildHEIF(1,
		[]heifItem{{id: 1, typ: "hvc1", props: []int{1, 2}, data: green}},
		[][]byte{cfg, ispe(128, 96)}, nil))

	// 2×1 grid of 64×96 tiles, green then red.
	cfgG, tileG := hevcFrame(t, "green", 64, 96)
	cfgR, tileR := hevcFrame(t, "red", 64, 96)
	grid := []byte{0, 0, 0, 1} // version, flags (16-bit sizes), rows-1, columns-1
	grid = append(grid, u16(128)...)
	grid = append(grid, u16(96)...)
	tiled := putFile(t, "grid.heic", buildHEIF(3,
		[]heifItem{
			{id: 1, typ: "hvc1", hidden: true, props: []int{1, 3}, data: tileG},
			{id: 2, typ: "hvc1", hidden: true, props: []int{2, 3}, data: tileR},
			{id: 3, typ: "grid", props: []int{4}, data: grid},
		},
		[][]byte{cfgG, cfgR, ispe(64, 96), ispe(128, 96)}, []int{1, 2}))

	for name, c := range map[string]struct {
		path  string
		left  color.Color
		right color.Color
	}{
		"single": {single, color.RGBA{0, 128, 0, 255}, color.RGBA{0, 128, 0, 255}},
		"grid":   {tiled, color.RGBA{0, 128, 0, 255}, red},
	} {
		t.Run(name, func(t *testing.T) {
			typ := sniffPath(t, c.path)
			if typ != (Type{"image/heic", KindImage, "mov"}) {
				t.Fatalf("sniff = %+v", typ)
			}
			info, err := r.Probe(testCtx(t), c.path, typ)
			if err != nil {
				t.Fatal(err)
			}
			if info.Width != 128 || info.Height != 96 || info.Animated {
				t.Errorf("Probe = %+v", info)
			}
			th, img := thumb(t, r, c.path)
			if th.MIME != "image/jpeg" || th.Width != 128 || th.Height != 96 {
				t.Errorf("thumb = %+v", th)
			}
			wantAt(t, img, 32, 48, c.left)
			wantAt(t, img, 96, 48, c.right)
		})
	}
}
