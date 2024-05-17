package streamdeck

import (
	"image"
	"time"
)

// DeviceInterface는 Stream Deck 장치를 나타내는 인터페이스입니다.
type DeviceInterface interface {
	// Open은 장치를 입력/출력용으로 엽니다. 장치와 통신하기 전에 호출해야 합니다.
	Open() error
	// Close는 장치와의 연결을 닫습니다.
	Close() error
	// FirmwareVersion은 장치의 펌웨어 버전을 반환합니다.
	FirmwareVersion() (string, error)
	// Reset은 Stream Deck를 재설정하고 모든 버튼 이미지를 지우고 대기 이미지를 표시합니다.
	Reset() error
	// Clear는 Stream Deck를 지우고 모든 버튼에 검은 이미지를 설정합니다.
	Clear() error
	// ReadKeys는 키 누름/뗌을 발생시킬 채널을 반환합니다.
	ReadKeys() (chan Key, error)
	// Sleep은 장치를 수면 모드로 전환하여 키 이벤트를 기다립니다.
	Sleep() error
	// Wake은 장치를 수면 모드에서 깨웁니다.
	Wake() error
	// Asleep은 장치가 수면 모드인지 여부를 반환합니다.
	Asleep() bool
	// SetSleepFadeDuration은 장치가 수면 모드로 들어가거나 깨어날 때 페이드 애니메이션의 지속 시간을 설정합니다.
	SetSleepFadeDuration(t time.Duration)
	// SetSleepTimeout은 키 이벤트를 받지 않을 경우 장치가 수면 모드로 들어갈 시간을 설정합니다.
	SetSleepTimeout(t time.Duration)
	// Fade는 밝기를 서서히 켜거나 끕니다.
	Fade(start uint8, end uint8, duration time.Duration) error
	// SetBrightness는 배경 조명 밝기를 0에서 100%까지 설정합니다.
	SetBrightness(percent uint8) error
	// SetImage는 Stream Deck의 버튼 이미지를 설정합니다. 제공된 이미지는 장치의 올바른 해상도여야 합니다. 인덱스는 왼쪽 위 버튼부터 0부터 시작합니다.
	SetImage(index uint8, img image.Image) error

	GetSerial() string
	GetKeys() uint8
	GetID() string
	GetPixels() uint
	GetDPI() uint
	GetPadding() uint
	GetColumns() uint8
	GetRows() uint8
	Flush() error
}
