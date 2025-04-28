package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"gopkg.in/yaml.v2"

	bubblesCommon "github.com/mritd/bubbles/common"
	selectorBubble "github.com/mritd/bubbles/selector"
	"github.com/renbou/jogmock/activities"
	"github.com/renbou/jogmock/fit-encoder/encoding"
	autoPromptBubble "github.com/renbou/jogmock/jogmock-cli/pkg/bubbles/autoprompt"
	promptBubble "github.com/renbou/jogmock/jogmock-cli/pkg/bubbles/prompt"
	"github.com/renbou/jogmock/jogmock-cli/pkg/bubbles/strava"
	stravaBubble "github.com/renbou/jogmock/jogmock-cli/pkg/bubbles/strava"
	"github.com/renbou/jogmock/strava-mock/stravapi"
	"github.com/spf13/cobra"
)

type activityConfig struct {
	CommonSpeed     *activities.SpeedOptions `yaml:"common_speed_options"`
	RareSpeed       *activities.SpeedOptions `yaml:"rare_speed_options"`
	RareSpeedChance float64                  `yaml:"rare_speed_chance"`
	FadeDuration    int                      `yaml:"fade_duration"`
	FadeFraction    float64                  `yaml:"fade_fraction"`
}

type dateTimeConfig struct {
	From string `yaml:"from"`
	To   string `yaml:"to"`
}

type speedConfig struct {
	Max float64 `yaml:"max"`
	Min float64 `yaml:"min"`
}

type startTimeConfig struct {
	From string `yaml:"from"`
	To   string `yaml:"to"`
}

type UserConfig struct {
	UsersConfig        []string            `yaml:"users"`
	DateTimeConfig     *dateTimeConfig     `yaml:"date_time"`
	StartTimeConfig    *startTimeConfig    `yaml:"start_time"`
	SpeedConfig        *speedConfig        `yaml:"speed"`
	StravaConfig       *stravapi.ApiConfig `yaml:"strava"`
	RunActivityConfig  *activityConfig     `yaml:"run_activity"`
	RideActivityConfig *activityConfig     `yaml:"ride_activity"`
}

// Arguments represents the possible commmand-line arguments
type Arguments struct {
	ConfigPath string
	OutputPath string
}

// LoadConfig reads and returns the config defined by args
func (args *Arguments) LoadConfig() (*UserConfig, error) {
	configFile, err := os.Open(args.ConfigPath)
	if err != nil {
		return nil, err
	}
	decoder := yaml.NewDecoder(configFile)

	config := new(UserConfig)
	if err := decoder.Decode(config); err != nil {
		return nil, err
	}

	if config.UsersConfig == nil {
		return nil, errors.New("users config is null or empty")
	}

	if config.DateTimeConfig == nil {
		return nil, errors.New("date_time config is null")
	}

	if config.DateTimeConfig.From == "" {
		return nil, errors.New("date_time from config is empty")
	}

	if config.DateTimeConfig.To == "" {
		return nil, errors.New("date_time to config is empty")
	}

	from, err := strToTimestamp(config.DateTimeConfig.From + " 00:00:00")
	if err != nil {
		return nil, err
	}
	to, err := strToTimestamp(config.DateTimeConfig.To + " 00:00:00")
	if err != nil {
		return nil, err
	}
	if from.Unix() > to.Unix() {
		return nil, errors.New("date_time from > to")
	}

	if config.SpeedConfig == nil {
		return nil, errors.New("speed config to config is empty")
	}

	return config, nil
}

func (args *Arguments) SaveConfig(config *UserConfig) error {
	configFile, err := os.Create(args.ConfigPath)
	if err != nil {
		return err
	}

	encoder := yaml.NewEncoder(configFile)
	return encoder.Encode(config)
}

type viewer interface {
	View() string
}

type modelStep struct {
	model viewer
	after func(value interface{})
}

func (s modelStep) Update(msg tea.Msg) (cmd tea.Cmd) {
	if msg == bubblesCommon.DONE {
		if s.after != nil {
			switch model := s.model.(type) {
			case interface{ Value() interface{} }:
				s.after(model.Value())
			case interface{ Value() string }:
				s.after(model.Value())
			case interface{ Selected() interface{} }:
				s.after(model.Selected())
			default:
				s.after(nil)
			}
		}
		return nil
	} else {
		switch model := s.model.(type) {
		case *promptBubble.Model:
			_, cmd = model.Update(msg)
		case *selectorBubble.Model:
			_, cmd = model.Update(msg)
		case *autoPromptBubble.Model:
			_, cmd = model.Update(msg)
		case tea.Model:
			_, cmd = model.Update(msg)
		default:
			panic(fmt.Sprintf("unknown model: %v", s.model))
		}
	}
	return
}

func (s modelStep) View() string {
	return s.model.View()
}

type simpleModel interface {
	Update(tea.Msg) tea.Cmd
	View() string
}

type ActivityModel struct {
	args        *Arguments
	config      *UserConfig
	options     activities.ActivityOptions
	gpxFilePath string
	steps       []simpleModel
	index       int
}

func strToTimestamp(val string) (time.Time, error) {
	t, err := time.Parse("02.01.2006 15:04:05", val)
	if err != nil {
		return time.Time{}, err
	}

	// offset the time by local timezone offset before sending it as timestamp
	_, offset := time.Now().Zone()
	return t.Add(-time.Second * time.Duration(offset)), nil
}

const (
	OkPrefix   = "[+]"
	ErrPrefix  = "[-]"
	ColorInfo  = "2"
	ColorWarn  = "#ed6c02"
	ColorError = "#d32f2f"
)

func NewActivityModel(config *UserConfig, gpxFilePath string, speed float64, start time.Time) *ActivityModel {

	model := &ActivityModel{
		args:   &arguments,
		config: config,
	}
	model.steps = []simpleModel{
		modelStep{
			&stravaBubble.Model{
				ActivityOptions: &model.options,
				ApiConfig:       model.config.StravaConfig,
				GpxFilePath:     &model.gpxFilePath,
				OutputPath:      &model.args.OutputPath,
			},
			func(value interface{}) {
				if value != nil {
					model.config.StravaConfig = value.(*stravapi.ApiConfig)
				}
			},
		},
	}
	//
	model.gpxFilePath = gpxFilePath
	//
	model.options.DesiredSpeed = speed
	// Set default value for activity
	activityCfg := model.config.RunActivityConfig
	model.options.Type = activities.RunActivity
	if activityCfg != nil {
		model.options.CommonSpeed = activityCfg.CommonSpeed
		model.options.RareSpeed = activityCfg.RareSpeed
		model.options.RareSpeedChance = activityCfg.RareSpeedChance
		model.options.FadeDuration = time.Duration(activityCfg.FadeDuration) * time.Second
		model.options.FadeFraction = activityCfg.FadeFraction
	}
	// Set default value for start time
	model.options.Start = start

	return model
}

func (m *ActivityModel) Init() tea.Cmd {
	return nil
}

var updateViewMsg tea.Msg = "UPDATE_VIEW"

func updateView() tea.Msg {
	return updateViewMsg
}

func (m *ActivityModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// otherwise if simply reading values
	switch msg {
	case updateViewMsg:
		return m, tea.Quit
	case bubblesCommon.DONE:
		m.steps[m.index].Update(bubblesCommon.DONE)
		m.index++
		if m.index < len(m.steps) {
			// initialize the next prompt
			return m, m.steps[m.index].Update(nil)
		}
		return m, updateView
	}
	return m, m.steps[m.index].Update(msg)
}

func (m *ActivityModel) View() string {
	until := m.index
	if until == len(m.steps) {
		until -= 1
	}

	var view string
	for i := 0; i <= until; i++ {
		view += m.steps[i].View()
	}
	return view
}

var (
	rootCmd = &cobra.Command{
		Use:   "jogmock",
		Short: "jogmock is a sophisticated activity faker for Strava",
		Long:  "a fake Strava client built using the knowledge gained from reverse engineering the mobile app",
		Run:   run,
	}
	arguments Arguments
)

func run(cmd *cobra.Command, args []string) {
	config, err := arguments.LoadConfig()
	if err != nil {
		fmt.Println(bubblesCommon.FontColor(ErrPrefix+" Unable to load config: "+err.Error(), ColorError))
		return
	}

	//
	from, _ := strToTimestamp(config.DateTimeConfig.From + " 00:00:00")
	to, _ := strToTimestamp(config.DateTimeConfig.To + " 00:00:00")
	from = from.In(time.Local)
	to = to.In(time.Local)
	fmt.Println(bubblesCommon.FontColor(OkPrefix+" From: "+from.Format("02/01/2006")+" to: "+to.Format("02/01/2006"), ColorInfo))
	//
	speed := config.SpeedConfig
	//
	// Chỉ định đường dẫn thư mục
	dir := "./gpxs" // Thay "path/to/your/folder" bằng đường dẫn thư mục của bạn
	var gpxFiles []string

	// Duyệt qua tất cả các tệp trong thư mục
	err = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		// Kiểm tra nếu tệp có phần mở rộng .gpx
		if !info.IsDir() && strings.HasSuffix(info.Name(), ".gpx") {
			gpxFiles = append(gpxFiles, path)
		}
		return nil
	})

	if err != nil {
		fmt.Println(bubblesCommon.FontColor(ErrPrefix+" Load gpx file in folder ./gpxs: "+err.Error(), ColorError))
		return
	}

	if len(gpxFiles) == 0 {
		fmt.Println(bubblesCommon.FontColor(ErrPrefix+" .gpx file not found in folder ./gpxs", ColorWarn))
		return
	}
	nowStr := time.Now().In(time.Local).Format("02.01.2006")
	fmt.Println(bubblesCommon.FontColor(OkPrefix+" Now: "+nowStr, ColorInfo))
	startTimeFrom, err := strToTimestamp(nowStr + " " + config.StartTimeConfig.From)
	if err != nil {
		fmt.Println(bubblesCommon.FontColor(ErrPrefix+" Convert start_time_form error: "+err.Error(), ColorError))
		return
	}
	startTimeTo, err := strToTimestamp(nowStr + " " + config.StartTimeConfig.To)
	if err != nil {
		fmt.Println(bubblesCommon.FontColor(ErrPrefix+" Convert start_time_to error: "+err.Error(), ColorError))
		return
	}
	startTimeFrom = startTimeFrom.In(time.Local)
	startTimeTo = startTimeTo.In(time.Local)

	if startTimeFrom.Unix() > startTimeTo.Unix() {
		fmt.Println(bubblesCommon.FontColor(ErrPrefix+" start_time_form > start_time_to", ColorError))
		return
	}

	second := startTimeTo.Unix() - startTimeFrom.Unix()

	users := config.UsersConfig
	for _, user := range users {
		fmt.Println(bubblesCommon.FontColor(OkPrefix+" User: "+user, ColorInfo))
		for cur := from.AddDate(0, 0, 0); cur.Unix() <= to.Unix(); cur = cur.AddDate(0, 0, 1) {
			fmt.Println(bubblesCommon.FontColor(OkPrefix+" \tDay: "+cur.Format("02/01/2006"), ColorInfo))
			speedRandomValue := speed.Min + rand.Float64()*(speed.Max-speed.Min)
			fmt.Println(bubblesCommon.FontColor(OkPrefix+fmt.Sprintf(" \t\tSpeed: %.3f", speedRandomValue), ColorInfo))
			gpxFileRandomValue := rand.Intn(len(gpxFiles))
			fmt.Println(bubblesCommon.FontColor(OkPrefix+fmt.Sprintf(" \t\tGPX file: %v", gpxFiles[gpxFileRandomValue]), ColorInfo))
			gpxFilePath := gpxFiles[gpxFileRandomValue]
			secodeRandomValue := rand.Int63n(second)
			fmt.Println(bubblesCommon.FontColor(OkPrefix+fmt.Sprintf(" \t\tSecodeRandomValue: %d", secodeRandomValue), ColorInfo))
			// startTimeFrom.Add(time.Duration(time.Duration(secodeRandomValue) * time.Second))
			start, _ := strToTimestamp(fmt.Sprintf("%v %v", cur.Format("02.01.2006"), startTimeFrom.Add(time.Duration(time.Duration(secodeRandomValue)*time.Second)).Format("15:04:05")))
			start = start.In(time.UTC)
			fmt.Println(bubblesCommon.FontColor(OkPrefix+fmt.Sprintf(" \t\tStart (Local): %v", start.In(time.Local).Format("2006-01-02 15:04:05")), ColorInfo))
			options := activities.ActivityOptions{
				Name:         "Run",
				Description:  "",
				Type:         activities.RunActivity,
				Start:        start,
				DesiredSpeed: speedRandomValue,
				CommonSpeed: &activities.SpeedOptions{
					Slope:       config.RunActivityConfig.CommonSpeed.Slope,
					Amplitude:   config.RunActivityConfig.CommonSpeed.Amplitude,
					MinDuration: config.RunActivityConfig.CommonSpeed.MinDuration,
					MaxDuration: config.RunActivityConfig.CommonSpeed.MaxDuration,
				},
				RareSpeed: &activities.SpeedOptions{
					Slope:       config.RunActivityConfig.RareSpeed.Slope,
					Amplitude:   config.RunActivityConfig.RareSpeed.Amplitude,
					MinDuration: config.RunActivityConfig.RareSpeed.MinDuration,
					MaxDuration: config.RunActivityConfig.RareSpeed.MaxDuration,
				},
				RareSpeedChance: config.RunActivityConfig.RareSpeedChance,
				FadeDuration:    time.Duration(config.RunActivityConfig.FadeDuration) * time.Second,
				FadeFraction:    config.RunActivityConfig.FadeFraction,
			}

			activity, err := activities.NewActivity(&options)

			if err != nil {
				fmt.Println(bubblesCommon.FontColor(ErrPrefix+" \t\tNew activity error: "+err.Error(), ColorWarn))
				continue
			}

			strava := strava.Model{
				ActivityOptions: &options,
				GpxFilePath:     &gpxFilePath,
			}

			strava.SetActivity(activity)

			err = strava.BuildActivity()

			if err != nil {
				fmt.Println(bubblesCommon.FontColor(ErrPrefix+" \t\tBuild activity error: "+err.Error(), ColorWarn))
				continue
			}

			fitFile, err := stravapi.BuildFitFile(activity)
			if err != nil {
				fmt.Println(bubblesCommon.FontColor(ErrPrefix+" \t\tBuild fit file error: "+err.Error(), ColorWarn))
				continue
			}

			activityBuffer := new(bytes.Buffer)
			encoder := encoding.NewEncoder(activityBuffer, encoding.BigEndian)
			if err := encoder.Encode(fitFile); err != nil {
				fmt.Println(bubblesCommon.FontColor(ErrPrefix+" \t\tEncode fit file error: "+err.Error(), ColorWarn))
				continue
			}

			// Lấy tên file từ đường dẫn
			fileName := filepath.Base(gpxFiles[gpxFileRandomValue])

			// Loại bỏ phần mở rộng (extension)
			fileNameWithoutExt := strings.TrimSuffix(fileName, filepath.Ext(fileName))

			savePath := fmt.Sprintf("output/%v/START_%v%v-SPEED_%.0f-GPX_%v.fit", user, cur.In(time.Local).Format("02012006"), start.In(time.Local).Format("150405"), speedRandomValue, fileNameWithoutExt)

			// Tạo thư mục nếu chưa tồn tại
			dir := filepath.Dir(savePath)
			if err := os.MkdirAll(dir, os.ModePerm); err != nil {
				fmt.Println(bubblesCommon.FontColor(ErrPrefix+" \t\tFailed to create directories: "+err.Error(), ColorWarn))
				continue
			}

			// Ghi buffer vào file
			outFile, err := os.Create(savePath)
			if err != nil {
				fmt.Println(bubblesCommon.FontColor(ErrPrefix+" \t\tFailed to create file: "+err.Error(), ColorWarn))
				continue
			}
			defer outFile.Close()

			if _, err := io.Copy(outFile, activityBuffer); err != nil {
				fmt.Println(bubblesCommon.FontColor(ErrPrefix+" \t\tFailed to write file: "+err.Error(), ColorWarn))
				continue
			}

			fmt.Println(bubblesCommon.FontColor(OkPrefix+fmt.Sprintf(" \t\tFit file: %v", savePath), ColorInfo))

			// Json File
			savePathJson := fmt.Sprintf("output/%v/START_%v%v-SPEED_%.0f-GPX_%v.json", user, cur.In(time.Local).Format("02012006"), start.In(time.Local).Format("150405"), speedRandomValue, fileNameWithoutExt)

			// Tạo thư mục nếu chưa tồn tại
			dirJson := filepath.Dir(savePathJson)
			if err := os.MkdirAll(dirJson, os.ModePerm); err != nil {
				fmt.Println(bubblesCommon.FontColor(ErrPrefix+" \t\tFailed to create directories: "+err.Error(), ColorWarn))
				continue
			}

			// Ghi buffer vào file
			outFileJson, err := os.Create(savePathJson)
			if err != nil {
				fmt.Println(bubblesCommon.FontColor(ErrPrefix+" \t\tFailed to create file: "+err.Error(), ColorWarn))
				continue
			}
			defer outFileJson.Close()

			encoderJson := json.NewEncoder(outFileJson)
			encoderJson.SetIndent("", "  ")
			records := activity.Records()
			err = encoderJson.Encode(records)
			if err != nil {
				fmt.Println(bubblesCommon.FontColor(ErrPrefix+" \t\tFailed to encode json file: "+err.Error(), ColorWarn))
				continue
			}

			fmt.Println(bubblesCommon.FontColor(OkPrefix+fmt.Sprintf(" \t\tJson file: %v", savePathJson), ColorInfo))

		}
	}

}

func init() {
	rootCmd.Flags().StringVar(&arguments.ConfigPath, "config", "config.yml", "path to config file")
	rootCmd.Flags().StringVar(&arguments.OutputPath, "output", "",
		"path where to output the created route instead of uploading")
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(ErrPrefix+" "+err.Error(), ColorError)
	}
}
