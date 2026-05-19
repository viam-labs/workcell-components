package main

import (
	wcc "workcellcomponents"

	"go.viam.com/rdk/components/generic"
	"go.viam.com/rdk/module"
	"go.viam.com/rdk/resource"
	"go.viam.com/rdk/services/worldstatestore"
)

func main() {
	module.ModularMain(
		resource.APIModel{API: generic.API, Model: wcc.PalletModel},
		resource.APIModel{API: generic.API, Model: wcc.PickStationModel},
		resource.APIModel{API: worldstatestore.API, Model: wcc.WorkcellSceneModel},
	)
}
