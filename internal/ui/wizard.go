package ui

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"

	"codeberg.org/nathawat/lecturejet/internal/config"
)

func Configure(in io.Reader, out io.Writer, cfg config.Config, choices []VAAPIChoice) (config.Config, error) {
	reader := bufio.NewReader(in)
	if len(choices) > 0 {
		fmt.Fprintln(out, "GPU video codec:")
		for i, choice := range choices {
			name := "H.264 / AVC"
			if choice.Codec == config.VideoHEVC {
				name = "HEVC / H.265"
			}
			fmt.Fprintf(out, "  [%d] %s\n", i+1, name)
		}
		value, err := readChoice(reader, out, "Choice", 1, len(choices))
		if err != nil {
			return config.Config{}, err
		}
		cfg.VideoCodec = choices[value-1].Codec
		cfg.VAAPIDevice = choices[value-1].Device
		if cfg.VideoCodec == config.VideoHEVC {
			fmt.Fprintln(out, "HEVC codec tag:")
			fmt.Fprintln(out, "  [1] hev1  default")
			fmt.Fprintln(out, "  [2] hvc1  Apple-compatible")
			tagChoice, err := readChoice(reader, out, "Choice", 1, 2)
			if err != nil {
				return config.Config{}, err
			}
			if tagChoice == 2 {
				cfg.HEVCCodecTag = config.HEVCCodecTagHVC1
			} else {
				cfg.HEVCCodecTag = config.HEVCCodecTagHEV1
			}
		}
	} else {
		fmt.Fprintln(out, "No usable VAAPI encoder found for selected input.")
	}

	choice, err := readLine(reader, out, "Use RNNoise noise reduction? [y/N] ")
	if err != nil {
		return config.Config{}, err
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(choice)), "y") {
		cfg.Noise.Enabled = true
		fmt.Fprintln(out, "Noise reduction strength:")
		fmt.Fprintln(out, "  [1] Maximum 100% (default)")
		fmt.Fprintln(out, "  [2] Strong  90%")
		fmt.Fprintln(out, "  [3] Strong  80%")
		fmt.Fprintln(out, "  [4] Custom")
		strengthChoice, err := readChoice(reader, out, "Choice", 1, 4)
		if err != nil {
			return config.Config{}, err
		}
		switch strengthChoice {
		case 1:
			cfg.Noise.Strength = 100
		case 2:
			cfg.Noise.Strength = 90
		case 3:
			cfg.Noise.Strength = 80
		case 4:
			value, err := readInt(reader, out, "Strength 0-100 [100]: ", 100)
			if err != nil {
				return config.Config{}, err
			}
			cfg.Noise.Strength = value
		}
		model, err := readLine(reader, out, "RNNoise model path: ")
		if err != nil {
			return config.Config{}, err
		}
		cfg.Noise.Model = strings.TrimSpace(model)
	} else {
		cfg.Noise.Enabled = false
	}

	prompt := "Convert output audio to mono? [Y/n] "
	if cfg.Noise.Enabled {
		prompt = "Convert output audio to mono? (RNNoise requires mono) [Y/n] "
	}
	mono, err := readLine(reader, out, prompt)
	if err != nil {
		return config.Config{}, err
	}
	cfg.Audio.Mono = !strings.HasPrefix(strings.ToLower(strings.TrimSpace(mono)), "n")
	return cfg, nil
}

func readChoice(reader *bufio.Reader, out io.Writer, prompt string, min, max int) (int, error) {
	return readInt(reader, out, fmt.Sprintf("%s [%d]: ", prompt, min), min, min, max)
}

func readInt(reader *bufio.Reader, out io.Writer, prompt string, fallback int, bounds ...int) (int, error) {
	line, err := readLine(reader, out, prompt)
	if err != nil {
		return 0, err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(line)
	if err != nil {
		return 0, fmt.Errorf("invalid number %q", line)
	}
	if len(bounds) == 2 && (value < bounds[0] || value > bounds[1]) {
		return 0, fmt.Errorf("choice must be between %d and %d", bounds[0], bounds[1])
	}
	return value, nil
}

func readLine(reader *bufio.Reader, out io.Writer, prompt string) (string, error) {
	fmt.Fprint(out, prompt)
	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return line, nil
}
