package configure

import (
	"fmt"
	"sort"
	"strings"

	"github.com/evcc-io/evcc/provider/mqtt"
	"github.com/evcc-io/evcc/util"
	"github.com/evcc-io/evcc/util/sponsor"
	"github.com/evcc-io/evcc/util/templates"
	stripmd "github.com/writeas/go-strip-markdown"
	"golang.org/x/exp/slices"
	"gopkg.in/yaml.v3"
)

// processDeviceSelection processes the user-selected device, checks
// if it's an actual device and makes sure the requirements are set
func (c *CmdConfigure) processDeviceSelection(deviceCategory DeviceCategory) (templates.Template, error) {
	templateItem := c.selectItem(deviceCategory)

	if templateItem.Title() == c.localizedString("ItemNotPresent", nil) {
		return templateItem, c.errItemNotPresent
	}

	if err := c.processDeviceRequirements(templateItem); err != nil {
		return templateItem, err
	}

	return templateItem, nil
}

// processDeviceValues processes the user provided values, creates
// a device configuration and check if it is a valid device
func (c *CmdConfigure) processDeviceValues(values map[string]interface{}, templateItem templates.Template, device device, deviceCategory DeviceCategory) (device, error) {
	c.addedDeviceIndex++

	device.Name = fmt.Sprintf("%s%d", DeviceCategories[deviceCategory].defaultName, c.addedDeviceIndex)
	device.Title = templateItem.Title()
	for item, value := range values {
		if strings.ToLower(item) != "title" {
			continue
		}
		if len(value.(string)) > 0 {
			device.Title = value.(string)
		}
	}

	var categoryWithUsage bool

	fmt.Println()
	switch deviceCategory {
	case DeviceCategoryPVMeter, DeviceCategoryBatteryMeter, DeviceCategoryGridMeter:
		categoryWithUsage = true
		fmt.Println(c.localizedString("TestingDevice_TitleUsage", localizeMap{"Device": templateItem.Title(), "Usage": deviceCategory.String()}))
	default:
		fmt.Println(c.localizedString("TestingDevice_Title", localizeMap{"Device": templateItem.Title()}))
	}

	deviceTest := DeviceTest{
		DeviceCategory: deviceCategory,
		Template:       templateItem,
		ConfigValues:   values,
	}

	testResult, err := deviceTest.Test()
	if err != nil {
		fmt.Println("  ", c.localizedString("Error", localizeMap{"Error": err}))
		fmt.Println()

		question := c.localizedString("TestingDevice_AddFailed", localizeMap{"Device": templateItem.Title()})
		if categoryWithUsage {
			question = c.localizedString("TestingDevice_AddFailedUsage", localizeMap{"Device": templateItem.Title(), "Usage": deviceCategory.String()})
		}
		if !c.askYesNo(question) {
			c.addedDeviceIndex--
			return device, c.errDeviceNotValid
		}
	} else {
		if deviceCategory == DeviceCategoryCharger && testResult == DeviceTestResultValid {
			device.ChargerHasMeter = true
		}
	}

	templateItem.Params = append(templateItem.Params, templates.Param{Name: "name", Value: device.Name})
	if !c.expandedMode {
		for _, param := range templateItem.Params {
			param.Help.Shorten(c.lang)
		}

		b, err := templateItem.RenderProxyWithValues(values, c.lang)
		if err != nil {
			c.addedDeviceIndex--
			return device, err
		}

		device.Yaml = string(b)
	} else {
		for _, p := range templateItem.Params {
			if p.Name == "name" {
				values["name"] = p.Value
				templateItem.Render = fmt.Sprintf("name: {{ .name }}\n%s", templateItem.Render)
			}
		}

		b, _, err := templateItem.RenderResult(templates.TemplateRenderModeInstance, values)
		if err != nil {
			c.addedDeviceIndex--
			return device, err
		}

		device.Yaml = string(b)
	}

	return device, nil
}

func (c *CmdConfigure) processDeviceCapabilities(capabilitites []string) {
	if slices.Contains(capabilitites, templates.CapabilitySMAHems) {
		c.capabilitySMAHems = true
	}
}

// processDeviceRequirements handles device requirements
func (c *CmdConfigure) processDeviceRequirements(templateItem templates.Template) error {
	requirementDescription := stripmd.Strip(templateItem.Requirements.Description.String(c.lang))
	if len(requirementDescription) > 0 {
		fmt.Println()
		fmt.Println("-------------------------------------------------")
		fmt.Println(c.localizedString("Requirements_Title", nil))
		fmt.Println(requirementDescription)
		if len(templateItem.Requirements.URI) > 0 {
			fmt.Println("  " + c.localizedString("Requirements_More", nil) + " " + templateItem.Requirements.URI)
		}
		fmt.Println("-------------------------------------------------")
	}

	// check if sponsorship is required
	if slices.Contains(templateItem.Requirements.EVCC, templates.RequirementSponsorship) && c.configuration.config.SponsorToken == "" {
		if err := c.askSponsortoken(true, false); err != nil {
			return err
		}
	}

	// check if we need to setup an MQTT broker
	if slices.Contains(templateItem.Requirements.EVCC, templates.RequirementMQTT) {
		if c.configuration.config.MQTT == "" {
			mqttConfig, err := c.configureMQTT(templateItem)
			if err != nil {
				return err
			}

			mqttYaml, err := yaml.Marshal(mqttConfig)
			if err != nil {
				return err
			}

			c.configuration.config.MQTT = string(mqttYaml)
		}
	}

	// check if we need to setup an EEBUS HEMS
	if slices.Contains(templateItem.Requirements.EVCC, templates.RequirementEEBUS) {
		if c.configuration.config.EEBUS == "" {
			fmt.Println()
			fmt.Println("-- EEBUS -----------------------------------")
			fmt.Println()
			eebusConfig, err := c.eebusCertificate()
			if err != nil {
				return fmt.Errorf("%s: %s", c.localizedString("Requirements_EEBUS_Cert_Error", nil), err)
			}

			if err := c.configureEEBus(eebusConfig); err != nil {
				return err
			}

			eebusYaml, err := yaml.Marshal(eebusConfig)
			if err != nil {
				return err
			}

			c.configuration.config.EEBUS = string(eebusYaml)
			fmt.Println()
			fmt.Println("--------------------------------------------")
		}

		fmt.Println()
		fmt.Println(c.localizedString("Requirements_EEBUS_Pairing", nil))
		fmt.Scanln()
	}

	return nil
}

// processParamRequirements handles param requirements
func (c *CmdConfigure) processParamRequirements(param templates.Param) error {
	requirementDescription := stripmd.Strip(param.Requirements.Description.String(c.lang))
	if len(requirementDescription) > 0 {
		fmt.Println()
		fmt.Println("-------------------------------------------------")
		fmt.Println(c.localizedString("Requirements_Title", nil))
		fmt.Println(requirementDescription)
		if len(param.Requirements.URI) > 0 {
			fmt.Println("  " + c.localizedString("Requirements_More", nil) + " " + param.Requirements.URI)
		}
		fmt.Println("-------------------------------------------------")
	}

	// check if sponsorship is required
	if slices.Contains(param.Requirements.EVCC, templates.RequirementSponsorship) && c.configuration.config.SponsorToken == "" {
		if err := c.askSponsortoken(true, true); err != nil {
			return err
		}
	}

	return nil
}

func (c *CmdConfigure) askSponsortoken(required bool, feature bool) error {
	fmt.Println("-- Sponsorship -----------------------------")
	if required {
		fmt.Println()
		if feature {
			fmt.Println(c.localizedString("Requirements_Sponsorship_Feature_Title", nil))
		} else {
			fmt.Println(c.localizedString("Requirements_Sponsorship_Title", nil))
		}
	} else {
		fmt.Println()
		fmt.Println(c.localizedString("Requirements_Sponsorship_Optional_Title", nil))
	}
	fmt.Println()
	if !c.askYesNo(c.localizedString("Requirements_Sponsorship_Token", nil)) {
		fmt.Println()
		fmt.Println("--------------------------------------------")
		return c.errItemNotPresent
	}

	sponsortoken := c.askValue(question{
		label:    c.localizedString("Requirements_Sponsorship_Token_Input", nil),
		mask:     true,
		required: true})

	err := sponsor.ConfigureSponsorship(sponsortoken)
	if err != nil {
		question := c.localizedString("TestingDevice_AddFailed", localizeMap{"Device": "Sponsorship Token"})
		if c.askYesNo(question) {
			err = nil
		}
	}
	if err == nil {
		c.configuration.config.SponsorToken = sponsortoken
	}

	fmt.Println()
	fmt.Println("--------------------------------------------")

	return err
}

func (c *CmdConfigure) configureMQTT(templateItem templates.Template) (map[string]interface{}, error) {
	fmt.Println()
	fmt.Println("-- MQTT Broker ----------------------------")

	var err error

	for ok := true; ok; {
		fmt.Println()
		_, paramHost := templateItem.ConfigDefaults.ParamByName("host")
		_, paramPort := templateItem.ConfigDefaults.ParamByName("port")
		_, paramUser := templateItem.ConfigDefaults.ParamByName("user")
		_, paramPassword := templateItem.ConfigDefaults.ParamByName("password")
		host := c.askValue(question{
			label:    paramHost.Description.String(c.lang),
			mask:     false,
			required: true})

		port := c.askValue(question{
			label:    paramPort.Description.String(c.lang),
			mask:     false,
			required: true})

		user := c.askValue(question{
			label:    paramUser.Description.String(c.lang),
			mask:     false,
			required: false})

		password := c.askValue(question{
			label:    paramPassword.Description.String(c.lang),
			mask:     true,
			required: false})

		fmt.Println()
		fmt.Println("--------------------------------------------")

		broker := fmt.Sprintf("%s:%s", host, port)

		mqttConfig := map[string]interface{}{
			"broker":   broker,
			"user":     user,
			"password": password,
		}

		log := util.NewLogger("mqtt")

		if mqtt.Instance, err = mqtt.RegisteredClient(log, broker, user, password, "", 1, false); err == nil {
			return mqttConfig, nil
		}

		fmt.Println()
		question := c.localizedString("TestingMQTTFailed", nil)
		if !c.askYesNo(question) {
			return nil, fmt.Errorf("failed configuring mqtt: %w", err)
		}
	}

	return nil, fmt.Errorf("failed configuring mqtt: %w", err)
}

// fetchElements returns template items of a given class
func (c *CmdConfigure) fetchElements(deviceCategory DeviceCategory) []templates.Template {
	var items []templates.Template
	for _, tmpl := range templates.ByClass(DeviceCategories[deviceCategory].class.String()) {
		if len(tmpl.Params) == 0 {
			continue
		}

		for _, t := range tmpl.Titles(c.lang) {
			titleTmpl := templates.Template{
				TemplateDefinition: tmpl.TemplateDefinition,
				ConfigDefaults:     tmpl.ConfigDefaults,
				Lang:               c.lang,
			}
			title := t
			groupTitle := titleTmpl.GroupTitle()
			if groupTitle != "" {
				title += " [" + groupTitle + "]"
			}
			titleTmpl.SetTitle(title)

			if deviceCategory == DeviceCategoryGuidedSetup {
				if tmpl.GuidedSetup.Enable {
					items = append(items, titleTmpl)
				}
			} else {
				if len(DeviceCategories[deviceCategory].categoryFilter) == 0 ||
					c.paramChoiceContains(tmpl.Params, templates.ParamUsage, DeviceCategories[deviceCategory].categoryFilter.String()) {
					items = append(items, titleTmpl)
				}
			}
		}
	}

	sort.Slice(items[:], func(i, j int) bool {
		// sort generic templates to the bottom
		if items[i].Group != "" && items[j].Group == "" {
			return false
		}
		if items[i].Group == "" && items[j].Group != "" {
			return true
		}
		if items[i].Group != items[j].Group {
			return strings.ToLower(items[i].GroupTitle()) < strings.ToLower(items[j].GroupTitle())
		}
		return strings.ToLower(items[i].Title()) < strings.ToLower(items[j].Title())
	})

	return items
}

// paramChoiceContains is a helper function to check if a param choice contains a given value
func (c *CmdConfigure) paramChoiceContains(params []templates.Param, name, filter string) bool {
	choices := c.paramChoiceValues(params, name)

	return slices.Contains(choices, filter)
}

// paramChoiceValues provides a list of possible values for a param choice
func (c *CmdConfigure) paramChoiceValues(params []templates.Param, name string) []string {
	choices := []string{}

	for _, item := range params {
		if item.Name != name {
			continue
		}

		choices = append(choices, item.Choice...)
	}

	return choices
}

// processConfig processes an EVCC configuration item
// Returns:
//   a map with param name and values
func (c *CmdConfigure) processConfig(templateItem *templates.Template, deviceCategory DeviceCategory) map[string]interface{} {
	fmt.Println()
	fmt.Println(c.localizedString("Config_Title", nil))
	fmt.Println()

	c.processModbusConfig(templateItem, deviceCategory)

	return c.processParams(templateItem, deviceCategory)
}

// process a list of params
func (c *CmdConfigure) processParams(templateItem *templates.Template, deviceCategory DeviceCategory) map[string]interface{} {
	usageFilter := DeviceCategories[deviceCategory].categoryFilter

	additionalConfig := make(map[string]interface{})

	for _, param := range templateItem.Params {
		if param.Dependencies != nil {
			valid := true
			for _, dep := range param.Dependencies {
				i, valueParam := templateItem.ParamByName(dep.Name)
				if i == -1 {
					break
				}

				value := valueParam.Value
				switch dep.Check {
				case templates.DependencyCheckEmpty:
					if additionalConfig[dep.Name] != nil {
						valid = additionalConfig[dep.Name] == ""
					} else {
						valid = value == ""
					}
				case templates.DependencyCheckNotEmpty:
					if additionalConfig[dep.Name] != nil {
						valid = additionalConfig[dep.Name] != ""
					} else {
						valid = value != ""
					}
				case templates.DependencyCheckEqual:
					if additionalConfig[dep.Name] != nil {
						valid = additionalConfig[dep.Name] == dep.Value
					} else {
						valid = value == dep.Value
					}
				}
				if !valid {
					break
				}
			}
			if !valid {
				continue
			}
		}

		switch param.Name {
		case templates.ParamModbus:
			additionalConfig[param.Name] = param.Value

		case templates.ParamUsage:
			if usageFilter != "" {
				additionalConfig[param.Name] = usageFilter.String()
			}

		default:
			if param.Advanced && !c.advancedMode || param.Deprecated {
				continue
			}

			if param.Hidden && param.Default != "" {
				additionalConfig[param.Name] = param.Default
				continue
			}

			switch param.ValueType {
			case templates.ParamValueTypeStringList:
				values := c.processListInputConfig(param)
				var nonEmptyValues []string
				for _, value := range values {
					if value != "" {
						nonEmptyValues = append(nonEmptyValues, value)
					}
				}
				additionalConfig[param.Name] = nonEmptyValues

			default:
				// TODO make processInputConfig aware of default values added by template
				if value := c.processInputConfig(param); value != "" {
					additionalConfig[param.Name] = value
				}
			}
		}
	}

	return additionalConfig
}

// handle user input of multiple items in a list
func (c *CmdConfigure) processListInputConfig(param templates.Param) []string {
	var values []string

	// ask for values until the user decides to stop
	for ok := true; ok; {
		newValue := c.processInputConfig(param)
		values = append(values, newValue)

		if newValue == "" {
			break
		}

		if !c.askYesNo("  " + c.localizedString("Config_AddAnotherValue", nil)) {
			break
		}
	}

	return values
}

// handle user input for a simple one value input
func (c *CmdConfigure) processInputConfig(param templates.Param) string {
	label := param.Name
	langLabel := param.Description.String(c.lang)
	if langLabel != "" {
		label = langLabel
	}

	help := param.Help.String(c.lang)
	if slices.Contains(param.Requirements.EVCC, templates.RequirementSponsorship) {
		help = fmt.Sprintf("%s\n\n%s", help, c.localizedString("Requirements_Sponsorship_Feature_Title", nil))
	}

	value := c.askValue(question{
		label:        label,
		defaultValue: param.Default,
		exampleValue: param.Example,
		help:         help,
		valueType:    param.ValueType,
		validValues:  param.ValidValues,
		mask:         param.Mask,
		required:     param.Required})

	if param.ValueType == templates.ParamValueTypeBool && value == "true" {
		if err := c.processParamRequirements(param); err != nil {
			return "false"
		}
	}

	return value
}

// handle user input for a device modbus configuration
func (c *CmdConfigure) processModbusConfig(templateItem *templates.Template, deviceCategory DeviceCategory) {
	var choices []string
	var choiceTypes []string

	modbusIndex, modbusParam := templateItem.ParamByName(templates.ParamModbus)
	if modbusIndex == -1 {
		return
	}

	config := templateItem.ConfigDefaults.Modbus

	for _, choice := range modbusParam.Choice {
		if config.Interfaces[choice] == nil {
			continue
		}

		for _, itype := range config.Interfaces[choice] {
			title := config.Types[itype].Description
			choices = append(choices, title.String(c.lang))
			choiceTypes = append(choiceTypes, itype)
		}
	}

	if len(choices) == 0 {
		return
	}

	// ask for modbus interface type
	var index int
	if len(choices) > 1 {
		index, _ = c.askChoice(c.localizedString("Config_ModbusInterface", nil), choices)
	}

	values := make(map[string]interface{})
	templateItem.Params[modbusIndex].Value = choiceTypes[index]

	// add the interface type specific modbus params
	templateItem.ModbusParams(choiceTypes[index], values)

	// update the modbus default values
	templateItem.ModbusValues(templates.TemplateRenderModeInstance, values)
}
