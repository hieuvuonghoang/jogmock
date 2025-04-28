package stravapi

import (
	"strconv"

	"github.com/renbou/jogmock/activities"
	"github.com/renbou/jogmock/fit-encoder/fit"
	"github.com/renbou/jogmock/strava-mock/internal/stravafit"
)

func BuildFitFile(activity *activities.Activity) (*fit.FitFile, error) {

	a := stravafit.StravaActivity{
		AppVersion:         1223885,
		MobileAppVersion:   "248.8 (1223885)",
		DeviceManufacturer: "Xiaomi",
		DeviceModel:        "Redmi Note 9 Pro",
		DeviceOsVersion:    strconv.Itoa(11),
		Activity:           activity,
	}
	fitFile, err := a.BuildFitFile()
	if err != nil {
		return nil, err
	}
	return fitFile, nil
}
