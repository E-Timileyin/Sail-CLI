package cmd

import "fmt"

const (
	BrightCyan    = "\x1b[96m"
	BrightGreen   = "\x1b[92m"
	BrightMagenta = "\x1b[95m"
	Reset         = "\x1b[0m"
	Bold          = "\x1b[1m"
)

func PrintBanner() {
	// Paste your ASCII art inside the backticks
	art := `
#     /$$   /$$                            /$$$$$$            /$$ /$$
#    | $$  | $$                           /$$__  $$          |__/| $$
#    | $$  | $$  /$$$$$$$  /$$$$$$       | $$  \__/  /$$$$$$  /$$| $$
#    | $$  | $$ /$$_____/ /$$__  $$      |  $$$$$$  |____  $$| $$| $$
#    | $$  | $$|  $$$$$$ | $$$$$$$$       \____  $$  /$$$$$$$| $$| $$
#    | $$  | $$ \____  $$| $$_____/       /$$  \ $$ /$$__  $$| $$| $$
#    |  $$$$$$/ /$$$$$$$/|  $$$$$$$      |  $$$$$$/|  $$$$$$$| $$| $$
#     \______/ |_______/  \_______/       \______/  \_______/|__/|__/
#
#
#
`
	fmt.Println(BrightCyan + art + Reset)

}
