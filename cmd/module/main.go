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
		// Core workcell components.
		resource.APIModel{API: generic.API, Model: wcc.PalletModel},
		resource.APIModel{API: generic.API, Model: wcc.PickStationModel},
		resource.APIModel{API: worldstatestore.API, Model: wcc.WorkcellSceneModel},

		// Safety hardware affordances.
		resource.APIModel{API: generic.API, Model: wcc.SafetyFenceModel},
		resource.APIModel{API: generic.API, Model: wcc.LightCurtainModel},
		resource.APIModel{API: generic.API, Model: wcc.EStopModel},
		resource.APIModel{API: generic.API, Model: wcc.StackLightModel},

		// Decoration affordances.
		resource.APIModel{API: generic.API, Model: wcc.ToteStackModel},
		resource.APIModel{API: generic.API, Model: wcc.RobotPedestalModel},
		resource.APIModel{API: generic.API, Model: wcc.HMICabinetModel},
		resource.APIModel{API: generic.API, Model: wcc.FloorDecalModel},
		resource.APIModel{API: generic.API, Model: wcc.WorkcellBoundsModel},
	)
}
