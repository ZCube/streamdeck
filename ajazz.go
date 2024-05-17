package streamdeck

import (
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"log"
	"math"
	"runtime"
	"runtime/debug"
	"sync"
	"time"

	retry "github.com/avast/retry-go/v4"
	"github.com/karalabe/hid"
	"golang.org/x/image/draw"
)

// DeviceAjazz represents a single Stream Deck device.
type DeviceAjazz struct {
	ID     string
	Serial string

	Columns uint8
	Rows    uint8
	Keys    uint8
	Pixels  uint
	DPI     uint
	Padding uint

	featureReportSize int
	keyStateOffset    int
	translateKeyIndex func(index, columns uint8) uint8
	flipImage         func(image.Image) image.Image
	toImageFormat     func(image.Image) ([]byte, error)

	device hid.Device
	info   hid.DeviceInfo

	lastActionTime time.Time
	asleep         bool
	sleepCancel    context.CancelFunc
	sleepMutex     *sync.RWMutex
	fadeDuration   time.Duration

	brightness         uint8
	preSleepBrightness uint8

	mutex *sync.Mutex
	cmd   []byte
	cmd1  []byte
	zero  []byte
}

// Open the device for input/output. This must be called before trying to
// communicate with the device.
func (d *DeviceAjazz) Open() error {
	var err error
	d.lastActionTime = time.Now()
	d.sleepMutex = &sync.RWMutex{}
	d.mutex = &sync.Mutex{}
	d.cmd = make([]byte, 512)
	d.cmd1 = make([]byte, 513)
	d.zero = make([]byte, 512)
	d.device, err = d.info.Open()
	if err != nil {
		return err
	}
	var version string
	version, err = d.FirmwareVersion()
	if err != nil {
		return err
	}
	fmt.Println("Firmware version:", version)

	err = d.cmdStopRetry(3)
	if err != nil {
		return err
	}

	err = d.cmdLightRetry(100, 3)
	if err != nil {
		return err
	}

	err = d.cmdClearRetry(0xff, 3)
	if err != nil {
		return err
	}

	return err
}

// Close the connection with the device.
func (d *DeviceAjazz) Close() error {
	d.cancelSleepTimer()
	d.cmdExit()
	return d.device.Close()
}

// FirmwareVersion returns the firmware version of the device.
func (d DeviceAjazz) FirmwareVersion() (string, error) {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	req := make([]byte, 512)
	req[0] = 0x01
	n, err := d.device.GetFeatureReport(req)
	if err != nil {
		return "", err
	}
	return string(req[:n]), err
}

func (d DeviceAjazz) cmdWrite(data []byte) error {
	var ptr []byte
	if runtime.GOOS != "windows" && data[0] == 0 {
		ptr = make([]byte, 513)
		ptr[0] = 0x00
		copy(ptr[1:], data)
		if (len(data) + 1) < 513 {
			copy(ptr[len(data)+1:], d.zero[:513-1-len(data)])
		}
	} else {
		ptr = make([]byte, 512)
		copy(ptr, data)
		if len(data) < 512 {
			copy(ptr[len(data):], d.zero[:512-len(data)])
		}
	}
	_, err := d.device.Write(ptr)
	return err
}

func (d DeviceAjazz) WriteRetry(data []byte) error {
	err := retry.Do(
		func() error {
			err := d.cmdWrite(data)
			if err != nil {
				log.Println(err)
				return err
			}
			return nil
		},
		retry.Attempts(3),
	)
	return err
}

func (d DeviceAjazz) cmdLightRetry(brightness uint8, retryAttempts uint) error {
	err := retry.Do(
		func() error {
			return d.cmdLight(brightness)
		},
		retry.Attempts(retryAttempts),
	)
	return err
}

func (d DeviceAjazz) cmdHangRetry(retryAttempts uint) error {
	err := retry.Do(
		func() error {
			return d.cmdHang()
		},
		retry.Attempts(retryAttempts),
	)
	return err
}

func (d DeviceAjazz) cmdLogoRetry(data []byte, retryAttempts uint) error {
	err := retry.Do(
		func() error {
			return d.cmdLogo(data)
		},
		retry.Attempts(retryAttempts),
	)
	return err
}

func (d DeviceAjazz) cmdClearRetry(target uint8, retryAttempts uint) error {
	err := retry.Do(
		func() error {
			return d.cmdClear(target)
		},
		retry.Attempts(retryAttempts),
	)
	return err
}

func (d DeviceAjazz) cmdStopRetry(retryAttempts uint) error {
	err := retry.Do(
		func() error {
			return d.cmdStop()
		},
		retry.Attempts(retryAttempts),
	)
	return err
}

func (d DeviceAjazz) cmdLight(brightness uint8) error {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	copy(d.cmd, d.zero)
	header := []byte{
		0x43, 0x52, 0x54, 0x00, 0x00, 0x4c, 0x49, 0x47, 0x00, 0x00,
	}
	copy(d.cmd, header)
	d.cmd[10] = brightness
	err := d.WriteRetry(d.cmd)
	return err
}

func (d DeviceAjazz) cmdHang() error {
	d.mutex.Lock()
	defer d.mutex.Unlock()

	// to logo screen
	// 0040   43 52 54 00 00 43 4c 45 00 00 44 43 00 00 00 00

	copy(d.cmd, d.zero)
	header := []byte{
		0x43, 0x52, 0x54, 0x00, 0x00, 0x43, 0x4c, 0x45, 0x00, 0x00, 0x44, 0x43, 0x00, 0x00, 0x00, 0x00,
	}
	copy(d.cmd, header)
	err := d.WriteRetry(d.cmd)
	if err != nil {
		return err
	}

	copy(d.cmd, d.zero)
	header = []byte{
		0x43, 0x52, 0x54, 0x00, 0x00, 0x48, 0x41, 0x4e,
	}
	copy(d.cmd, header)
	err = d.WriteRetry(d.cmd)
	return err
}

func (d DeviceAjazz) cmdLogo(data []byte) error {
	d.mutex.Lock()
	defer d.mutex.Unlock()

	if len(data) != 854*480*3 {
		return fmt.Errorf("invalid data length: %d", len(data))
	}
	copy(d.cmd, d.zero)
	header := []byte{
		0x43, 0x52, 0x54, 0x00, 0x00, 0x4c, 0x4f, 0x47, 0x00, 0x12, 0xc3, 0xc0, 0x01, 0x00, 0x00, 0x00,
	}
	// rgb 854 * 480 * 3
	copy(d.cmd, header)
	size := len(data)
	for i := 0; i < size; i += 512 {
		if i+512 < size {
			ptr := data[i : i+512]
			err := d.cmdWrite(ptr)
			if err != nil {
				log.Print(err)
				// ignore err on mac
				return err
			}
		} else {
			ptr := data[i:]
			err := d.cmdWrite(ptr)
			if err != nil {
				fmt.Println(err)
				// ignore err on mac
				return err
			}
		}
	}
	return nil
}

func (d DeviceAjazz) cmdClear(target uint8) error {
	// fmt.Println("cmdClear", target)
	if target != 0xff {
		target = elgato_to_ajazz(target, d.Columns)
	}
	d.mutex.Lock()
	defer d.mutex.Unlock()
	copy(d.cmd, d.zero)
	header := []byte{
		0x43, 0x52, 0x54, 0x00, 0x00, 0x43, 0x4c, 0x45, 0x00, 0x00, 0x00, 0xff, 0x00, 0x00, 0x00, 0x00,
	}
	// to logo screen
	// 0040   43 52 54 00 00 43 4c 45 00 00 44 43 00 00 00 00

	// rgb 845 * 480 * 3
	copy(d.cmd, header)
	d.cmd[11] = target
	err := d.WriteRetry(d.cmd)
	return err
}

func (d DeviceAjazz) cmdExit() error {
	// fmt.Println("cmdExit")
	d.mutex.Lock()
	defer d.mutex.Unlock()
	copy(d.cmd, d.zero)
	header := []byte{
		0x43, 0x52, 0x54, 0x00, 0x00, 0x43, 0x4c, 0x45, 0x00, 0x00, 0x44, 0x43, 0x00, 0x00, 0x00, 0x00,
	}

	// rgb 845 * 480 * 3
	copy(d.cmd, header)
	err := d.WriteRetry(d.cmd)
	return err
}

func (d DeviceAjazz) cmdStop() error {
	// fmt.Println("cmdStop")
	d.mutex.Lock()
	defer d.mutex.Unlock()
	copy(d.cmd, d.zero)
	header := []byte{
		0x43, 0x52, 0x54, 0x00, 0x00, 0x53, 0x54, 0x50,
	}
	copy(d.cmd, header)
	err := d.WriteRetry(d.cmd)
	return err
}

func (d DeviceAjazz) cmdBatchRetry(index uint8, data []byte, retryAttempts uint) error {
	err := retry.Do(
		func() error {
			// fmt.Println("cmdBatch", target, len(data))
			target := elgato_to_ajazz(index, d.Columns)
			d.mutex.Lock()
			defer d.mutex.Unlock()
			copy(d.cmd, d.zero)
			header := []byte{
				0x43, 0x52, 0x54, 0x00, 0x00, 0x42, 0x41, 0x54, 0x00, 0x00, 0x0c, 0x48, 0x0d, 0x00, 0x00, 0x00,
			}
			copy(d.cmd, header)
			d.cmd[12] = target
			size := len(data)
			binary.BigEndian.PutUint32(d.cmd[8:], uint32(size))
			err := d.cmdWrite(d.cmd)
			if err != nil {
				log.Print(err)
				debug.PrintStack()
				// ignore err on mac
				return err
			}

			for i := 0; i < size; i += 512 {
				if i+512 < size {
					ptr := data[i : i+512]
					err := d.cmdWrite(ptr)
					if err != nil {
						log.Print(err)
						// ignore err on mac
						return err
					}
				} else {
					ptr := data[i:]
					err := d.cmdWrite(ptr)
					if err != nil {
						fmt.Println(err)
						// ignore err on mac
						return err
					}
				}
			}
			return nil
		},
		retry.OnRetry(func(n uint, err error) {
			d.cmdClear(index)
		}),
		retry.Attempts(retryAttempts),
	)
	return err
}

func (d DeviceAjazz) cmdBatch(target uint8, data []byte) error {
	// fmt.Println("cmdBatch", target, len(data))
	target = elgato_to_ajazz(target+1, d.Columns)
	d.mutex.Lock()
	defer d.mutex.Unlock()
	copy(d.cmd, d.zero)
	header := []byte{
		0x43, 0x52, 0x54, 0x00, 0x00, 0x42, 0x41, 0x54, 0x00, 0x00, 0x0c, 0x48, 0x0d, 0x00, 0x00, 0x00,
	}
	copy(d.cmd, header)
	d.cmd[12] = target
	size := len(data)
	binary.BigEndian.PutUint32(d.cmd[8:], uint32(size))
	err := d.WriteRetry(d.cmd)
	if err != nil {
		log.Print(err)
		debug.PrintStack()
		// ignore err on mac
		return err
	}

	for i := 0; i < size; i += 512 {
		if i+512 < size {
			ptr := data[i : i+512]
			err := d.cmdWrite(ptr)
			if err != nil {
				log.Print(err)
				// ignore err on mac
				return err
			}
		} else {
			ptr := data[i:]
			err := d.cmdWrite(ptr)
			if err != nil {
				fmt.Println(err)
				// ignore err on mac
				return err
			}
		}
	}
	return nil
}

// Resets the Stream Deck, clears all button images and shows the standby image.
func (d DeviceAjazz) Reset() error {
	err := d.cmdStopRetry(3)
	if err != nil {
		return err
	}

	err = d.cmdLightRetry(100, 3)
	if err != nil {
		return err
	}

	err = d.cmdClearRetry(0xff, 3)
	if err != nil {
		return err
	}

	return nil
}

// Clears the Stream Deck, setting a black image on all buttons.
func (d DeviceAjazz) Clear() error {
	img := image.NewRGBA(image.Rect(0, 0, int(d.GetPixels()), int(d.GetPixels())))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{0, 0, 0, 255}), image.Point{}, draw.Src)
	for i := uint8(0); i <= d.Columns*d.Rows; i++ {
		err := d.SetImage(i, img)
		if err != nil {
			fmt.Println(err)
			return err
		}
	}

	return nil
}

// ReadKeys returns a channel, which it will use to emit key presses/releases.
func (d *DeviceAjazz) ReadKeys() (chan Key, error) {
	kch := make(chan Key)
	// return kch, nil
	keyBuffer := make([]byte, 512)
	go func() {
		for {
			if n, err := d.device.Read(keyBuffer); err != nil {
				close(kch)
				return
			} else if n <= 0 {
				continue
			}

			// don't trigger a key event if the device is asleep, but wake it
			if d.asleep {
				_ = d.Wake()

				continue
			}

			d.sleepMutex.Lock()
			d.lastActionTime = time.Now()
			d.sleepMutex.Unlock()

			{
				keyIndex := uint8(keyBuffer[9])
				kch <- Key{
					Index:   d.translateKeyIndex(keyIndex, d.Columns),
					Pressed: true,
				}
				kch <- Key{
					Index:   d.translateKeyIndex(keyIndex, d.Columns),
					Pressed: false,
				}
				keyBuffer[9] = 0

			}
		}
	}()

	return kch, nil
}

// Sleep puts the device asleep, waiting for a key event to wake it up.
func (d *DeviceAjazz) Sleep() error {
	d.sleepMutex.Lock()
	defer d.sleepMutex.Unlock()

	d.preSleepBrightness = d.brightness

	if err := d.Fade(d.brightness, 0, d.fadeDuration); err != nil {
		return err
	}

	d.asleep = true
	return d.SetBrightness(0)
}

// Wake wakes the device from sleep.
func (d *DeviceAjazz) Wake() error {
	d.sleepMutex.Lock()
	defer d.sleepMutex.Unlock()

	d.asleep = false
	if err := d.Fade(0, d.preSleepBrightness, d.fadeDuration); err != nil {
		return err
	}

	d.lastActionTime = time.Now()
	return d.SetBrightness(d.preSleepBrightness)
}

// Asleep returns true if the device is asleep.
func (d DeviceAjazz) Asleep() bool {
	return d.asleep
}

func (d *DeviceAjazz) cancelSleepTimer() {
	if d.sleepCancel == nil {
		return
	}

	d.sleepCancel()
	d.sleepCancel = nil
}

// SetSleepFadeDuration sets the duration of the fading animation when the
// device is put to sleep or wakes up.
func (d *DeviceAjazz) SetSleepFadeDuration(t time.Duration) {
	d.fadeDuration = t
}

// SetSleepTimeout sets the time after which the device will sleep if no key
// events are received.
func (d *DeviceAjazz) SetSleepTimeout(t time.Duration) {
	d.cancelSleepTimer()
	if t == 0 {
		return
	}

	var ctx context.Context
	ctx, d.sleepCancel = context.WithCancel(context.Background())

	go func() {
		for {
			select {
			case <-time.After(time.Second):
				d.sleepMutex.RLock()
				since := time.Since(d.lastActionTime)
				d.sleepMutex.RUnlock()

				if !d.asleep && since >= t {
					_ = d.Sleep()
				}

			case <-ctx.Done():
				return
			}
		}
	}()
}

// Fade fades the brightness in or out.
func (d *DeviceAjazz) Fade(start uint8, end uint8, duration time.Duration) error {
	step := (float64(end) - float64(start)) / float64(duration/fadeDelay)
	if step == math.Inf(1) || step == math.Inf(-1) {
		return nil
	}

	for current := float64(start); ; current += step {
		if !((start < end && int8(current) < int8(end)) ||
			(start > end && int8(current) > int8(end))) {
			break
		}
		if err := d.SetBrightness(uint8(current)); err != nil {
			return err
		}

		time.Sleep(fadeDelay)
	}
	return nil
}

// SetBrightness sets the background lighting brightness from 0 to 100 percent.
func (d *DeviceAjazz) SetBrightness(percent uint8) error {
	if percent > 100 {
		percent = 100
	}

	d.brightness = percent
	if d.asleep && percent > 0 {
		// if the device is asleep, remember the brightness, but don't set it
		d.sleepMutex.Lock()
		d.preSleepBrightness = percent
		d.sleepMutex.Unlock()
		return nil
	}

	return d.cmdLightRetry(d.brightness, 3)
}

// SetImage sets the image of a button on the Stream Deck. The provided image
// needs to be in the correct resolution for the device. The index starts with
// 0 being the top-left button.
func (d DeviceAjazz) SetImage(index uint8, img image.Image) error {
	if img.Bounds().Dy() != int(d.GetPixels()) ||
		img.Bounds().Dx() != int(d.GetPixels()) {
		return fmt.Errorf("supplied image has wrong dimensions, expected %[1]dx%[1]d pixels", d.GetPixels())
	}

	imageBytes, err := d.toImageFormat(d.flipImage(img))
	if err != nil {
		return fmt.Errorf("cannot convert image data: %v", err)
	}

	err = d.cmdBatchRetry(index, imageBytes, 3)
	if err != nil {
		return fmt.Errorf("cannot send image data: %v", err)
	}
	// err = d.cmdStop()
	// if err != nil {
	// 	return fmt.Errorf("cannot send image data: %v", err)
	// }

	return nil
}

func (d DeviceAjazz) GetSerial() string {
	return d.Serial
}

func (d DeviceAjazz) GetKeys() uint8 {
	return d.Keys
}

func (d DeviceAjazz) GetID() string {
	return d.ID
}

func (d DeviceAjazz) GetPixels() uint {
	return d.Pixels
}

func (d DeviceAjazz) GetDPI() uint {
	return d.DPI
}

func (d DeviceAjazz) GetPadding() uint {
	return d.Padding
}

func (d DeviceAjazz) GetColumns() uint8 {
	return d.Columns
}

func (d DeviceAjazz) GetRows() uint8 {
	return d.Rows
}

func (d DeviceAjazz) Flush() error {
	// fmt.Println("flushing...")
	// d.cmdClear(0x00)
	return d.cmdStopRetry(3)
}

/*
	Ajazz's key index
	-----------------------------

| 0d | 0a | 07 | 04 | 01 | 10 |
|----|----|----|----|----|----|
| 0e | 0b | 08 | 05 | 02 | 11 |
|----|----|----|----|----|----|
| 0f | 0c | 09 | 06 | 03 | 12 |

	-----------------------------
	Elgato's key index
	-----------------------------

| 01 | 02 | 03 | 04 | 05 | 06 |
|----|----|----|----|----|----|
| 07 | 08 | 09 | 10 | 11 | 12 |
|----|----|----|----|----|----|
| 13 | 14 | 15 | 16 | 17 | 18 |

	-----------------------------
*/
func elgato_to_ajazz(index, columns uint8) uint8 {
	if index > 17 {
		return index
	}
	return []uint8{12, 9, 6, 3, 0, 15, 13, 10, 7, 4, 1, 16, 14, 11, 8, 5, 2, 17}[index] + 1
}

func ajazz_to_elgato(index, columns uint8) uint8 {
	if index > 18 {
		return index
	}
	return []uint8{4, 10, 16, 3, 9, 15, 2, 8, 14, 1, 7, 13, 0, 6, 12, 5, 11, 17}[index-1]
}
