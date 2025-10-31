# go-qrcode #

<img src='https://skip.org/img/nyancat-youtube-qr.png' align='right'>

Package qrcode implements a QR Code encoder. [![Build Status](https://travis-ci.org/skip2/go-qrcode.svg?branch=master)](https://travis-ci.org/skip2/go-qrcode)

A QR Code is a matrix (two-dimensional) barcode. Arbitrary content may be encoded, with URLs being a popular choice :)

Each QR Code contains error recovery information to aid reading damaged or obscured codes. There are four levels of error recovery: Low, medium, high and highest. QR Codes with a higher recovery level are more robust to damage, at the cost of being physically larger.

## Install

    go get -u github.com/D4ario0/go-qrcode/...

A command-line tool `qrcode` will be built into `$GOPATH/bin/`.

## Usage

    import qrcode "github.com/D4ario0/go-qrcode"

- **Create a 256x256 PNG image:**

        var png []byte
        png, err := qrcode.Encode("https://example.org", qrcode.Medium, 256)

- **Create a 256x256 PNG image and write to a file:**

        err := qrcode.WriteFile("https://example.org", qrcode.Medium, 256, "qr.png")

- **Create a 256x256 PNG image with custom colors and write to file:**

        err := qrcode.WriteColorFile("https://example.org", qrcode.Medium, 256, color.Black, color.White, "qr.png")

- **Create a QR code with custom roundness and quiet zone:**

        qr, _ := qrcode.New("https://example.org", qrcode.Medium)
        qr.SetRoundness(0.7) // defaults to 0 (square); 1 fully rounds outer corners
        qr.SetQuietZone(2)   // defaults to 4 modules; use 0 to remove the border
        png, err := qr.PNG(256)

## Library API

All helpers ultimately return a PNG `[]byte`, so you can stream to any io.Writer, store on disk, or respond over HTTP.

```go
png, err := qrcode.Encode("https://example.org", qrcode.Medium, 256)
if err != nil {
    log.Fatal(err)
}

if _, err := w.Write(png); err != nil { // w can be os.Stdout or http.ResponseWriter
    log.Fatal(err)
}
```

### Functional options

Configuration is expressed through functional options so the same parameters are available across `New`, `Encode`, `WriteFile`, and `WriteColorFile`:

| Option | Description |
| --- | --- |
| `WithRoundness(value float64)` | Sets module roundness. `0` keeps hard squares; `1` fully rounds exposed corners (values are clamped into `[0,1]`). |
| `WithQuietZone(size int)` | Overrides the quiet-zone width in modules. Use `-1` to restore the default (`4`), or `0` to remove it entirely. |
| `WithForegroundColor(color.Color)` / `WithBackgroundColor(color.Color)` | Override individual colors. |
| `WithColors(fg, bg color.Color)` | Convenience helper for setting both colors at once. |
| `WithBorderDisabled()` / `WithBorderEnabled()` | Explicitly toggle the border regardless of other settings. |

Example combining several options:

```go
qr, err := qrcode.New(
    "https://example.org",
    qrcode.High,
    qrcode.WithRoundness(1.0),
    qrcode.WithQuietZone(2),
    qrcode.WithColors(color.Black, color.RGBA{240, 240, 240, 255}),
)
if err != nil {
    log.Fatal(err)
}

png, err := qr.PNG(300)
if err != nil {
    log.Fatal(err)
}

if err := os.WriteFile("rounded.png", png, 0o644); err != nil {
    log.Fatal(err)
}
```

### Serving over HTTP

Stream PNG output directly to an `http.ResponseWriter` using the same option
set:

```go
func handler(w http.ResponseWriter, r *http.Request) {
    qr, err := qrcode.New(
        r.FormValue("payload"),
        qrcode.High,
        qrcode.WithRoundness(1.0),
        qrcode.WithQuietZone(2),
    )
    if err != nil {
        http.Error(w, "encode failed", http.StatusInternalServerError)
        return
    }

    w.Header().Set("Content-Type", "image/png")
    w.WriteHeader(http.StatusOK)

    if err := qr.Write(300, w); err != nil {
        http.Error(w, "stream failed", http.StatusInternalServerError)
    }
}
```

All examples use the qrcode.Medium error Recovery Level and create a fixed 256x256px size QR Code. The last function creates a white on black instead of black on white QR Code.

## Documentation

[![godoc](https://godoc.org/github.com/D4ario0/go-qrcode?status.png)](https://godoc.org/github.com/skip2/go-qrcode)



## CLI

A command-line tool `qrcode` will be built into `$GOPATH/bin/`.

```
qrcode -- QR Code encoder in Go
https://github.com/D4ario0/go-qrcode

Flags:
  -d	disable QR Code border
  -i	invert black and white
  -o string
     	out PNG file prefix, empty for stdout
  -quiet-zone int
    	quiet zone width in modules (default 4)
  -roundness float
    	module corner roundness (0=square, 1=fully rounded)
  -s int
     	image size (pixel) (default 256)
  -t	print as text-art on stdout

Usage:
  1. Arguments except for flags are joined by " " and used to generate QR code.
     Default output is STDOUT, pipe to imagemagick command "display" to display
     on any X server.

       qrcode hello word | display

  2. Save to file if "display" not available:

       qrcode "homepage: https://github.com/D4ario0/go-qrcode" > out.png

```
## Maximum capacity
The maximum capacity of a QR Code varies according to the content encoded and the error recovery level. The maximum capacity is 2,953 bytes, 4,296 alphanumeric characters, 7,089 numeric digits, or a combination of these.

## Borderless QR Codes

To aid QR Code reading software, QR codes have a built in whitespace border.

If you know what you're doing, and don't want a border, see https://gist.github.com/skip2/7e3d8a82f5317df9be437f8ec8ec0b7d for how to do it. It's still recommended you include a border manually.

## Links

- [http://en.wikipedia.org/wiki/QR_code](http://en.wikipedia.org/wiki/QR_code)
- [ISO/IEC 18004:2006](http://www.iso.org/iso/catalogue_detail.htm?csnumber=43655) - Main QR Code specification (approx CHF 198,00)<br>
- [https://github.com/qpliu/qrencode-go/](https://github.com/qpliu/qrencode-go/) - alternative Go QR encoding library based on [ZXing](https://github.com/zxing/zxing)
