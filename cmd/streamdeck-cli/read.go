package main

import (
	"fmt"

	"github.com/muesli/coral"
)

var (
	readCmd = &coral.Command{
		Use:   "read",
		Short: "read the device, clears all images and shows the default logo",
		RunE: func(cmd *coral.Command, args []string) error {
			kch, err := d.ReadKeys()
			if err != nil {
				return err
			}

			for {
				select {
				case k := <-kch:
					fmt.Printf("Key %d: %v\n", k.Index, k.Pressed)
				}
			}
			return nil
		},
	}
)

func init() {
	RootCmd.AddCommand(readCmd)
}
