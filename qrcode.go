// go-qrcode
// Copyright 2014 Tom Harwood

/*
Package qrcode implements a QR Code encoder.

A QR Code is a matrix (two-dimensional) barcode. Arbitrary content may be
encoded.

A QR Code contains error recovery information to aid reading damaged or
obscured codes. There are four levels of error recovery: qrcode.{Low, Medium,
High, Highest}. QR Codes with a higher recovery level are more robust to damage,
at the cost of being physically larger.

Three functions cover most use cases:

- Create a PNG image:

	var png []byte
	png, err := qrcode.Encode("https://example.org", qrcode.Medium, 256)

- Create a PNG image and write to a file:

	err := qrcode.WriteFile("https://example.org", qrcode.Medium, 256, "qr.png")

- Create a PNG image with custom colors and write to file:

	err := qrcode.WriteColorFile("https://example.org", qrcode.Medium, 256, color.Black, color.White, "qr.png")

All examples use the qrcode.Medium error Recovery Level and create a fixed
256x256px size QR Code. The last function creates a white on black instead of black
on white QR Code.

To generate a variable sized image instead, specify a negative size (in place of
the 256 above), such as -4 or -5. Larger negative numbers create larger images:
A size of -5 sets each module (QR Code "pixel") to be 5px wide/high.

- Create a PNG image (variable size, with minimum white padding) and write to a file:

	err := qrcode.WriteFile("https://example.org", qrcode.Medium, -5, "qr.png")

The maximum capacity of a QR Code varies according to the content encoded and
the error recovery level. The maximum capacity is 2,953 bytes, 4,296
alphanumeric characters, 7,089 numeric digits, or a combination of these.

This package implements a subset of QR Code 2005, as defined in ISO/IEC
18004:2006.
*/
package qrcode

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"io/ioutil"
	"log"
	"os"

	bitset "github.com/D4ario0/go-qrcode/bitset"
	reedsolomon "github.com/D4ario0/go-qrcode/reedsolomon"
)

// Option configures a QRCode after construction.
type Option func(*QRCode)

// WithRoundness sets the roundness value when creating a QRCode. The value is
// clamped to the range [0, 1].
func WithRoundness(roundness float64) Option {
	return func(q *QRCode) {
		q.SetRoundness(roundness)
	}
}

// WithQuietZone sets the quiet-zone size (in modules) when creating a QRCode.
// Pass -1 to restore the default (4 modules) or 0 to remove the border.
func WithQuietZone(size int) Option {
	return func(q *QRCode) {
		q.SetQuietZone(size)
	}
}

// Encode a QR Code and return a raw PNG image.
//
// size is both the image width and height in pixels. If size is too small then
// a larger image is silently returned. Negative values for size cause a
// variable sized image to be returned: See the documentation for Image().
//
// To serve over HTTP, remember to send a Content-Type: image/png header.
func Encode(content string, level RecoveryLevel, size int, opts ...Option) ([]byte, error) {
	var q *QRCode

	q, err := New(content, level, opts...)

	if err != nil {
		return nil, err
	}

	return q.PNG(size)
}

// WriteFile encodes, then writes a QR Code to the given filename in PNG format.
//
// size is both the image width and height in pixels. If size is too small then
// a larger image is silently written. Negative values for size cause a variable
// sized image to be written: See the documentation for Image().
func WriteFile(content string, level RecoveryLevel, size int, filename string, opts ...Option) error {
	var q *QRCode

	q, err := New(content, level, opts...)

	if err != nil {
		return err
	}

	return q.WriteFile(size, filename)
}

// WriteColorFile encodes, then writes a QR Code to the given filename in PNG format.
// With WriteColorFile you can also specify the colors you want to use.
//
// size is both the image width and height in pixels. If size is too small then
// a larger image is silently written. Negative values for size cause a variable
// sized image to be written: See the documentation for Image().
func WriteColorFile(content string, level RecoveryLevel, size int, background,
	foreground color.Color, filename string, opts ...Option) error {

	var q *QRCode

	q, err := New(content, level, opts...)

	if err != nil {
		return err
	}

	q.BackgroundColor = background
	q.ForegroundColor = foreground

	return q.WriteFile(size, filename)
}

// A QRCode represents a valid encoded QRCode.
type QRCode struct {
	// Original content encoded.
	Content string

	// QR Code type.
	Level         RecoveryLevel
	VersionNumber int

	// User settable drawing options.
	ForegroundColor color.Color
	BackgroundColor color.Color

	// Disable the QR Code border.
	DisableBorder bool

	// Roundness controls the radius of outer module corners, expressed as a
	// percentage of the module half-width. A value of 0 keeps classic square
	// modules. Values greater than 1 are clamped to 1.
	Roundness float64

	// QuietZoneSize overrides the module-width of the quiet zone. A value of -1
	// defers to the default specified by the QR version.
	QuietZoneSize int

	encoder *dataEncoder
	version qrCodeVersion

	data   *bitset.Bitset
	symbol *symbol
	mask   int
}

// SetRoundness switches module rendering from hard squares to squares with
// rounded outer corners. Values outside the range [0, 1] are clamped.
func (q *QRCode) SetRoundness(roundness float64) {
	q.Roundness = clampRoundness(roundness)
}

// SetQuietZone sets the quiet-zone width in modules. Values below zero are
// clamped to zero, except for -1 which restores the QR version default.
func (q *QRCode) SetQuietZone(size int) {
	if size < 0 {
		if size == -1 {
			q.QuietZoneSize = -1
		} else {
			q.QuietZoneSize = 0
		}
		return
	}
	q.QuietZoneSize = size
}

func clampRoundness(roundness float64) float64 {
	if roundness < 0 {
		return 0
	}
	if roundness > 1 {
		return 1
	}
	return roundness
}

func (q *QRCode) quietZoneModules() int {
	if q.DisableBorder {
		return 0
	}
	if q.QuietZoneSize >= 0 {
		return q.QuietZoneSize
	}
	return q.version.quietZoneSize()
}

// New constructs a QRCode.
//
//	var q *qrcode.QRCode
//	q, err := qrcode.New("my content", qrcode.Medium)
//
// An error occurs if the content is too long.
func New(content string, level RecoveryLevel, opts ...Option) (*QRCode, error) {
	encoders := []dataEncoderType{dataEncoderType1To9, dataEncoderType10To26,
		dataEncoderType27To40}

	var encoder *dataEncoder
	var encoded *bitset.Bitset
	var chosenVersion *qrCodeVersion
	var err error

	for _, t := range encoders {
		encoder = newDataEncoder(t)
		encoded, err = encoder.encode([]byte(content))

		if err != nil {
			continue
		}

		chosenVersion = chooseQRCodeVersion(level, encoder, encoded.Len())

		if chosenVersion != nil {
			break
		}
	}

	if err != nil {
		return nil, err
	} else if chosenVersion == nil {
		return nil, errors.New("content too long to encode")
	}

	q := &QRCode{
		Content: content,

		Level:         level,
		VersionNumber: chosenVersion.version,

		ForegroundColor: color.Black,
		BackgroundColor: color.White,
		QuietZoneSize:   -1,

		encoder: encoder,
		data:    encoded,
		version: *chosenVersion,
	}

	for _, opt := range opts {
		if opt != nil {
			opt(q)
		}
	}

	return q, nil
}

// NewWithForcedVersion constructs a QRCode of a specific version.
//
//	var q *qrcode.QRCode
//	q, err := qrcode.NewWithForcedVersion("my content", 25, qrcode.Medium)
//
// An error occurs in case of invalid version.
func NewWithForcedVersion(content string, version int, level RecoveryLevel, opts ...Option) (*QRCode, error) {
	var encoder *dataEncoder

	switch {
	case version >= 1 && version <= 9:
		encoder = newDataEncoder(dataEncoderType1To9)
	case version >= 10 && version <= 26:
		encoder = newDataEncoder(dataEncoderType10To26)
	case version >= 27 && version <= 40:
		encoder = newDataEncoder(dataEncoderType27To40)
	default:
		return nil, fmt.Errorf("Invalid version %d (expected 1-40 inclusive)", version)
	}

	var encoded *bitset.Bitset
	encoded, err := encoder.encode([]byte(content))

	if err != nil {
		return nil, err
	}

	chosenVersion := getQRCodeVersion(level, version)

	if chosenVersion == nil {
		return nil, errors.New("cannot find QR Code version")
	}

	if encoded.Len() > chosenVersion.numDataBits() {
		return nil, fmt.Errorf("Cannot encode QR code: content too large for fixed size QR Code version %d (encoded length is %d bits, maximum length is %d bits)",
			version,
			encoded.Len(),
			chosenVersion.numDataBits())
	}

	q := &QRCode{
		Content: content,

		Level:         level,
		VersionNumber: chosenVersion.version,

		ForegroundColor: color.Black,
		BackgroundColor: color.White,
		QuietZoneSize:   -1,

		encoder: encoder,
		data:    encoded,
		version: *chosenVersion,
	}

	for _, opt := range opts {
		if opt != nil {
			opt(q)
		}
	}

	return q, nil
}

// Bitmap returns the QR Code as a 2D array of 1-bit pixels.
//
// bitmap[y][x] is true if the pixel at (x, y) is set.
//
// The bitmap includes the required "quiet zone" around the QR Code to aid
// decoding.
func (q *QRCode) Bitmap() [][]bool {
	// Build QR code.
	q.encode()

	return q.symbol.bitmap()
}

// Image returns the QR Code as an image.Image.
//
// A positive size sets a fixed image width and height (e.g. 256 yields an
// 256x256px image).
//
// Depending on the amount of data encoded, fixed size images can have different
// amounts of padding (white space around the QR Code). As an alternative, a
// variable sized image can be generated instead:
//
// A negative size causes a variable sized image to be returned. The image
// returned is the minimum size required for the QR Code. Choose a larger
// negative number to increase the scale of the image. e.g. a size of -5 causes
// each module (QR Code "pixel") to be 5px in size.
func (q *QRCode) Image(size int) image.Image {
	// Build QR code.
	q.encode()

	// Minimum pixels (both width and height) required.
	realSize := q.symbol.size

	// Variable size support.
	if size < 0 {
		size = size * -1 * realSize
	}

	// Actual pixels available to draw the symbol. Automatically increase the
	// image size if it's not large enough.
	if size < realSize {
		size = realSize
	}

	// Output image.
	rect := image.Rectangle{Min: image.Point{0, 0}, Max: image.Point{size, size}}

	// Saves a few bytes to have them in this order
	p := color.Palette([]color.Color{q.BackgroundColor, q.ForegroundColor})
	img := image.NewPaletted(rect, p)
	fgClr := uint8(img.Palette.Index(q.ForegroundColor))

	// QR code bitmap.
	bitmap := q.symbol.bitmap()

	modulesPerPixel := float64(realSize) / float64(size)
	pixelsPerModule := float64(size) / float64(realSize)
	roundness := clampRoundness(q.Roundness)

	if roundness > 0 {
		radius := pixelsPerModule * 0.5 * roundness
		renderRoundedModules(img, bitmap, realSize, size, fgClr, pixelsPerModule, radius)
	} else {
		renderSquareModules(img, bitmap, realSize, size, fgClr, modulesPerPixel)
	}

	return img
}

// renderSquareModules fills each true bitmap module using a straightforward
// pixel-to-module mapping.
func renderSquareModules(img *image.Paletted, bitmap [][]bool, realSize, size int, fgClr uint8, modulesPerPixel float64) {
	for py := 0; py < size; py++ {
		moduleY := int(float64(py) * modulesPerPixel)
		if moduleY >= realSize {
			moduleY = realSize - 1
		}
		row := bitmap[moduleY]
		for px := 0; px < size; px++ {
			moduleX := int(float64(px) * modulesPerPixel)
			if moduleX >= realSize {
				moduleX = realSize - 1
			}
			if row[moduleX] {
				pos := img.PixOffset(px, py)
				img.Pix[pos] = fgClr
			}
		}
	}
}

// renderRoundedModules trims the outer corners of isolated modules to achieve
// a rounded appearance while keeping edges shared with neighbors square.
func renderRoundedModules(img *image.Paletted, bitmap [][]bool, realSize, size int, fgClr uint8, pixelsPerModule, radius float64) {
	radiusSquared := radius * radius

	for moduleY := 0; moduleY < realSize; moduleY++ {
		yStart, yEnd := modulePixelRange(moduleY, realSize, size)
		if yStart == yEnd {
			continue
		}
		yStartF := float64(moduleY) * pixelsPerModule
		yEndF := yStartF + pixelsPerModule

		row := bitmap[moduleY]
		for moduleX := 0; moduleX < realSize; moduleX++ {
			if !row[moduleX] {
				continue
			}

			xStart, xEnd := modulePixelRange(moduleX, realSize, size)
			if xStart == xEnd {
				continue
			}
			xStartF := float64(moduleX) * pixelsPerModule
			xEndF := xStartF + pixelsPerModule

			neighbors := moduleNeighborsAt(bitmap, moduleX, moduleY)

			for py := yStart; py < yEnd; py++ {
				pyCenter := float64(py) + 0.5
				for px := xStart; px < xEnd; px++ {
					pxCenter := float64(px) + 0.5

					if shouldTrimCorner(pxCenter, pyCenter, xStartF, xEndF, yStartF, yEndF, radius, radiusSquared, neighbors) {
						continue
					}

					pos := img.PixOffset(px, py)
					img.Pix[pos] = fgClr
				}
			}
		}
	}
}

func modulePixelRange(index, realSize, size int) (int, int) {
	start := index * size / realSize
	end := (index + 1) * size / realSize
	if end <= start {
		end = start + 1
	}
	if end > size {
		end = size
	}
	return start, end
}

type moduleNeighbors struct {
	top    bool
	bottom bool
	left   bool
	right  bool
}

func moduleNeighborsAt(bitmap [][]bool, x, y int) moduleNeighbors {
	neighbors := moduleNeighbors{}
	if y > 0 {
		neighbors.top = bitmap[y-1][x]
	}
	if y+1 < len(bitmap) {
		neighbors.bottom = bitmap[y+1][x]
	}
	if x > 0 {
		neighbors.left = bitmap[y][x-1]
	}
	if x+1 < len(bitmap[y]) {
		neighbors.right = bitmap[y][x+1]
	}
	return neighbors
}

func shouldTrimCorner(pxCenter, pyCenter, xStart, xEnd, yStart, yEnd, radius, radiusSquared float64, neighbors moduleNeighbors) bool {
	if radius <= 0 {
		return false
	}

	if !neighbors.top && !neighbors.left && pxCenter < xStart+radius && pyCenter < yStart+radius {
		return outsideCorner(pxCenter, pyCenter, xStart+radius, yStart+radius, radiusSquared)
	}

	if !neighbors.top && !neighbors.right && pxCenter > xEnd-radius && pyCenter < yStart+radius {
		return outsideCorner(pxCenter, pyCenter, xEnd-radius, yStart+radius, radiusSquared)
	}

	if !neighbors.bottom && !neighbors.left && pxCenter < xStart+radius && pyCenter > yEnd-radius {
		return outsideCorner(pxCenter, pyCenter, xStart+radius, yEnd-radius, radiusSquared)
	}

	if !neighbors.bottom && !neighbors.right && pxCenter > xEnd-radius && pyCenter > yEnd-radius {
		return outsideCorner(pxCenter, pyCenter, xEnd-radius, yEnd-radius, radiusSquared)
	}

	return false
}

func outsideCorner(pxCenter, pyCenter, cornerX, cornerY, radiusSquared float64) bool {
	dx := pxCenter - cornerX
	dy := pyCenter - cornerY
	return dx*dx+dy*dy > radiusSquared
}

// PNG returns the QR Code as a PNG image.
//
// size is both the image width and height in pixels. If size is too small then
// a larger image is silently returned. Negative values for size cause a
// variable sized image to be returned: See the documentation for Image().
func (q *QRCode) PNG(size int) ([]byte, error) {
	img := q.Image(size)

	encoder := png.Encoder{CompressionLevel: png.BestCompression}

	var b bytes.Buffer
	err := encoder.Encode(&b, img)

	if err != nil {
		return nil, err
	}

	return b.Bytes(), nil
}

// Write writes the QR Code as a PNG image to io.Writer.
//
// size is both the image width and height in pixels. If size is too small then
// a larger image is silently written. Negative values for size cause a
// variable sized image to be written: See the documentation for Image().
func (q *QRCode) Write(size int, out io.Writer) error {
	var png []byte

	png, err := q.PNG(size)

	if err != nil {
		return err
	}
	_, err = out.Write(png)
	return err
}

// WriteFile writes the QR Code as a PNG image to the specified file.
//
// size is both the image width and height in pixels. If size is too small then
// a larger image is silently written. Negative values for size cause a
// variable sized image to be written: See the documentation for Image().
func (q *QRCode) WriteFile(size int, filename string) error {
	var png []byte

	png, err := q.PNG(size)

	if err != nil {
		return err
	}

	return ioutil.WriteFile(filename, png, os.FileMode(0644))
}

// encode completes the steps required to encode the QR Code. These include
// adding the terminator bits and padding, splitting the data into blocks and
// applying the error correction, and selecting the best data mask.
func (q *QRCode) encode() {
	numTerminatorBits := q.version.numTerminatorBitsRequired(q.data.Len())

	q.addTerminatorBits(numTerminatorBits)
	q.addPadding()

	encoded := q.encodeBlocks()

	const numMasks int = 8
	penalty := 0

	for mask := 0; mask < numMasks; mask++ {
		var s *symbol
		var err error

		s, err = buildRegularSymbol(q.version, mask, encoded, q.quietZoneModules())

		if err != nil {
			log.Panic(err.Error())
		}

		numEmptyModules := s.numEmptyModules()
		if numEmptyModules != 0 {
			log.Panicf("bug: numEmptyModules is %d (expected 0) (version=%d)",
				numEmptyModules, q.VersionNumber)
		}

		p := s.penaltyScore()

		//log.Printf("mask=%d p=%3d p1=%3d p2=%3d p3=%3d p4=%d\n", mask, p, s.penalty1(), s.penalty2(), s.penalty3(), s.penalty4())

		if q.symbol == nil || p < penalty {
			q.symbol = s
			q.mask = mask
			penalty = p
		}
	}
}

// addTerminatorBits adds final terminator bits to the encoded data.
//
// The number of terminator bits required is determined when the QR Code version
// is chosen (which itself depends on the length of the data encoded). The
// terminator bits are thus added after the QR Code version
// is chosen, rather than at the data encoding stage.
func (q *QRCode) addTerminatorBits(numTerminatorBits int) {
	q.data.AppendNumBools(numTerminatorBits, false)
}

// encodeBlocks takes the completed (terminated & padded) encoded data, splits
// the data into blocks (as specified by the QR Code version), applies error
// correction to each block, then interleaves the blocks together.
//
// The QR Code's final data sequence is returned.
func (q *QRCode) encodeBlocks() *bitset.Bitset {
	// Split into blocks.
	type dataBlock struct {
		data          *bitset.Bitset
		ecStartOffset int
	}

	block := make([]dataBlock, q.version.numBlocks())

	start := 0
	end := 0
	blockID := 0

	for _, b := range q.version.block {
		for j := 0; j < b.numBlocks; j++ {
			start = end
			end = start + b.numDataCodewords*8

			// Apply error correction to each block.
			numErrorCodewords := b.numCodewords - b.numDataCodewords
			block[blockID].data = reedsolomon.Encode(q.data.Substr(start, end), numErrorCodewords)
			block[blockID].ecStartOffset = end - start

			blockID++
		}
	}

	// Interleave the blocks.

	result := bitset.New()

	// Combine data blocks.
	working := true
	for i := 0; working; i += 8 {
		working = false

		for j, b := range block {
			if i >= block[j].ecStartOffset {
				continue
			}

			result.Append(b.data.Substr(i, i+8))

			working = true
		}
	}

	// Combine error correction blocks.
	working = true
	for i := 0; working; i += 8 {
		working = false

		for j, b := range block {
			offset := i + block[j].ecStartOffset
			if offset >= block[j].data.Len() {
				continue
			}

			result.Append(b.data.Substr(offset, offset+8))

			working = true
		}
	}

	// Append remainder bits.
	result.AppendNumBools(q.version.numRemainderBits, false)

	return result
}

// max returns the maximum of a and b.
func max(a int, b int) int {
	if a > b {
		return a
	}

	return b
}

// addPadding pads the encoded data upto the full length required.
func (q *QRCode) addPadding() {
	numDataBits := q.version.numDataBits()

	if q.data.Len() == numDataBits {
		return
	}

	// Pad to the nearest codeword boundary.
	q.data.AppendNumBools(q.version.numBitsToPadToCodeword(q.data.Len()), false)

	// Pad codewords 0b11101100 and 0b00010001.
	padding := [2]*bitset.Bitset{
		bitset.New(true, true, true, false, true, true, false, false),
		bitset.New(false, false, false, true, false, false, false, true),
	}

	// Insert pad codewords alternately.
	i := 0
	for numDataBits-q.data.Len() >= 8 {
		q.data.Append(padding[i])

		i = 1 - i // Alternate between 0 and 1.
	}

	if q.data.Len() != numDataBits {
		log.Panicf("BUG: got len %d, expected %d", q.data.Len(), numDataBits)
	}
}

// ToString produces a multi-line string that forms a QR-code image.
func (q *QRCode) ToString(inverseColor bool) string {
	bits := q.Bitmap()
	var buf bytes.Buffer
	for y := range bits {
		for x := range bits[y] {
			if bits[y][x] != inverseColor {
				buf.WriteString("  ")
			} else {
				buf.WriteString("██")
			}
		}
		buf.WriteString("\n")
	}
	return buf.String()
}

// ToSmallString produces a multi-line string that forms a QR-code image, a
// factor two smaller in x and y then ToString.
func (q *QRCode) ToSmallString(inverseColor bool) string {
	bits := q.Bitmap()
	var buf bytes.Buffer
	// if there is an odd number of rows, the last one needs special treatment
	for y := 0; y < len(bits)-1; y += 2 {
		for x := range bits[y] {
			if bits[y][x] == bits[y+1][x] {
				if bits[y][x] != inverseColor {
					buf.WriteString(" ")
				} else {
					buf.WriteString("█")
				}
			} else {
				if bits[y][x] != inverseColor {
					buf.WriteString("▄")
				} else {
					buf.WriteString("▀")
				}
			}
		}
		buf.WriteString("\n")
	}
	// special treatment for the last row if odd
	if len(bits)%2 == 1 {
		y := len(bits) - 1
		for x := range bits[y] {
			if bits[y][x] != inverseColor {
				buf.WriteString(" ")
			} else {
				buf.WriteString("▀")
			}
		}
		buf.WriteString("\n")
	}
	return buf.String()
}
