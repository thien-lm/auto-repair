package manipulation

import (
	"github.com/vmware/go-vcloud-director/v2/govcd"
	"fmt"
	"errors"
	vcdObject "github.com/phongvu0403/secret-manager/vcd"
)

func RebootVM(client *govcd.VCDClient, org string, vdc string, vmName string) error {
	// Retrieve vDC
	vappName := getVAppNameFromVMName(vmName)
	vm, err := vcdObject.GetVMFromName(client, org, vdc, vappName, vmName)
	if err != nil {
		fmt.Printf("Error retrieving vm: %v\n", err)
		return errors.New("error retrieving vm")
	}
	
	// Send a poweroff request  VM
	taskpof, err := vm.PowerOff()
	if err != nil {
		fmt.Printf("Error powerOff VM: %v\n with task %v" , err, taskpof)
		return errors.New("error powerOff vm")
	}
	err = taskpof.WaitTaskCompletion()
	if err != nil {
		fmt.Printf("task power off %v failed", taskpof)
		return errors.New("error powerOff vm")
	}

	taskpon, err := vm.PowerOn()
	if err != nil {
		fmt.Printf("Error powerOn VM: %v\n with task %v" , err, taskpon)
		return errors.New("error powerOn vm")

	}
	err = taskpon.WaitTaskCompletion()
	if err != nil {
		fmt.Printf("task power on %v failed", taskpon)
		return errors.New("error powerOff vm")
	}

	fmt.Printf("reboot successfully")
	
	return nil
}

// func GetVMIDByVMName(client *govcd.Client, org string, vdc string, vmName string) {
// 	client.
// }