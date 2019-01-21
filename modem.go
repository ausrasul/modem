/*

Modem package allows you to manage usb modems connected to your computer.
Usage example:

	package main

	import "github.com/ausrasul/modem"
	import "time"
	import "fmt"



	func main() {
		m := modem.New()
		m.AddHandler(
			func(m modem.Modem){_ = m.Imei}, // handle modem Add
			func(m modem.Modem){_ = m.Tty}, // handle modem Update
			func(m modem.Modem){_ = m.Net}, // handle modem Remove
		)
		m.AddFilter("1199", "68a3")
		m.AddFilter("12d1", "1001")
		m.AddFilter("12d1", "1506")

		m.Monitor()
		for i := 0; i<60; i++{
			fmt.Println(m.List)
			time.Sleep(time.Second)
		}
		m.StopMonitor()
	}

*/
package modem

import "github.com/ausrasul/udev"
import "github.com/tarm/serial"
import "errors"
import "time"
import "strings"
const IMEILEN = 17

// USB Modem object
type Modem struct {
	Net   string
	Tty   string
	Imei  string
	ready int
}

type filter struct {
	vid string
	pid string
}

// USB Device Manager object
type Manager struct {
	filters     []filter
	devices     map[string]Modem
	stopMonitor chan bool
	monitoring bool
	handleAdd func(Modem)
	handleRemove func(Modem)
	handleUpdate func(Modem)
}

// Get new device manager instance
func New() *Manager {
	return &Manager{
		devices:     make(map[string]Modem),
		handleAdd: func(m Modem){_ = m},
		handleRemove: func(m Modem){_ = m},
		handleUpdate: func(m Modem){_ = m},
	}
}

func (m *Manager) AddHandler(add func(Modem), update func(Modem), remove func(Modem)){
	if add != nil {
		m.handleAdd = add
	}
	if update != nil {
		m.handleUpdate = update
	}
	if remove != nil {
		m.handleRemove = remove
	}
}

// Add Device Filter
func (m *Manager) AddFilter(vid string, pid string) {
	f := filter{vid: vid, pid: pid}
	m.filters = append(m.filters, f)
	return
}

// Returns a hashmap of connected USB modems and their IMEI
func (m *Manager) List() map[string]Modem {
	devList := make(map[string]Modem)
	for k, v := range m.devices {
		if v.ready == 1 {
			devList[k] = v
		}
	}
	return devList
}

// Start a monitor goroutine, Non blocking, you have to Unref the device manager to end it.
func (m *Manager) Monitor() error{
	if m.monitoring {
		return errors.New("Monitor is already started")
	}
	m.stopMonitor = make(chan bool)
	go m.monitor()
	return nil
}

// Stop the monitor goroutine and empty the device list.
func (m *Manager) StopMonitor() error {
	if !m.monitoring {
		return errors.New("Monitor already stopped.")
	}
	close(m.stopMonitor)
	m.monitoring = false
	return nil
}

func (m *Manager) monitor() {

	u := udev.NewUdev()
	defer u.Unref()

	e := u.NewEnumerate()
	defer e.Unref()

	mon := udev.NewMonitorFromNetlink(u, "udev")
	defer mon.Unref()
	
	mon.AddFilter("tty", "")
	mon.AddFilter("net", "")
	mon.AddFilter("usb", "usb_device")

	err := mon.EnableReceiving()
	if err != nil {
		return
	}

	e.AddMatchSubsystem("tty")
	e.AddMatchSubsystem("net")
	e.ScanDevices()

	for device := e.First(); !device.IsNil(); device = device.Next() {
		path := device.Name()
		dev := u.DeviceFromSysPath(path)
		m.readDevice(dev)
	}
	for {
		select {
		case <-m.stopMonitor:
			for k := range m.devices{
				delete(m.devices, k)
			}
			break
		default:
			d := mon.ReceiveDevice()
			if !d.IsNil() {
				m.readDevice(d)
			} else {
				time.Sleep(time.Second)
			}
		}
	}
	// then hold a list for usb plug ports status
	// map usbplug port number and status to the list.
}

// Reads a modem properties and attributes and add/remove it from the list of devices.
func (m *Manager) readDevice(dev *udev.Device) {
	action := dev.Action()
	
	// Handle Remove action
	if action == "remove" {
		modem, ok := m.devices[dev.DevNode()]
		if !ok {
			return
		}
		m.handleRemove(modem)
		delete(m.devices, dev.DevNode())
		return
	}

	fileDescriptor := dev.SysName()
	originalDevNode := dev.DevNode()
	originalSubSys := dev.Subsystem()
	originalEPnum := dev.Parent().Parent().SysAttrValue("bNumEndpoints")

	// Filter unrelated devices
	if originalSubSys != "tty" && originalSubSys != "net" {
		return
	}

	dev = dev.ParentWithSubsystemDevType("usb", "usb_device")
	if dev.IsNil() {
		return
	}

	vid := dev.SysAttrValue("idVendor")
	pid := dev.SysAttrValue("idProduct")
	for _, f := range m.filters {
		if vid == f.vid && pid == f.pid {
			d := m.devices[dev.DevNode()]
			if originalSubSys == "net" {
				d.Net = fileDescriptor
			}
			if originalSubSys == "tty" && originalEPnum == "03" {
				// Delay if add action
				if action == "add" {
					time.Sleep(time.Second * 5)
				}
				imei, err := getImei(originalDevNode)
				if err == nil {
					d.Tty = originalDevNode
					d.Imei = imei
					d.ready = 1
				}
				if (action != "update"){
					m.handleAdd(d)
				} else {
					m.handleUpdate(d)
				}

			}
			m.devices[dev.DevNode()] = d
		}
	}
}

// Get IMEI from a modem using AT command
func getImei(port string) (imei string, err error) {
	c := &serial.Config{Name: port, Baud: 115200, ReadTimeout: time.Millisecond * 10}
	s, err := serial.OpenPort(c)
	if err != nil {
		return
	}
	n, err := s.Write([]byte("AT+CGSN\r\n"))
	if err != nil {
		return
	}
	buf := make([]byte, 128)
	s.Read(buf)
	n, err = s.Read(buf)
	if err != nil {
		return
	}
	if n != 25 {
		return "", errors.New("Invalid Imei")
	}
	return strings.Trim(string(buf[:IMEILEN]), "\r\n "), nil
}
