/*
 * Tencent is pleased to support the open source community by making 蓝鲸 available.
 * Copyright (C) 2017-2018 THL A29 Limited, a Tencent company. All rights reserved.
 * Licensed under the MIT License (the "License"); you may not use this file except
 * in compliance with the License. You may obtain a copy of the License at
 * http://opensource.org/licenses/MIT
 * Unless required by applicable law or agreed to in writing, software distributed under
 * the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND,
 * either express or implied. See the License for the specific language governing permissions and
 * limitations under the License.
 */

package logics

import (
	"context"
	"fmt"
	"net/http"
	"time"

	mgo "gopkg.in/mgo.v2"

	"configcenter/src/common"
	"configcenter/src/common/blog"
	meta "configcenter/src/common/metadata"
	"configcenter/src/common/util"
)

func (lgc *Logics) AddProperty(
	pheader http.Header, propertyInfoList []meta.NetcollectProperty) ([]meta.AddNetPropertyResult, bool) {
	ownerID := util.GetOwnerID(pheader)

	resultList := make([]meta.AddNetPropertyResult, 0)
	hasError := false

	for _, propertyInfo := range propertyInfoList {
		errMsg := ""
		result := true

		propertyID, err := lgc.addProperty(pheader, propertyInfo, ownerID)
		if nil != err {
			errMsg = err.Error()
			result = false
			hasError = true
		}

		resultList = append(resultList, meta.AddNetPropertyResult{
			Result:               result,
			ErrMsg:               errMsg,
			NetcollectPropertyID: propertyID,
		})
	}

	return resultList, hasError
}

func (lgc *Logics) SearchProperty(pheader http.Header, params *meta.NetCollSearchParams) (*meta.SearchNetProperty, error) {
	defErr := lgc.Engine.CCErr.CreateDefaultCCErrorIf(util.GetLanguage(pheader))

	// classify condition
	deviceCond, objectCond, propertyCond, netPropertyCond := lgc.classifyNetPropertyCondition(params.Condition)

	searchResult := meta.SearchNetProperty{Count: 0, Info: []meta.NetcollectProperty{}}

	var (
		err                error
		objIDs             []string
		deviceIDs          []int64
		propertyIDs        []string
		showFields         netPropertyShowFields // to be displayed field of netProperty that be got from other tables
		objIDMapShowFields map[string]objShowField
	)
	// if property has filter condition
	if 0 < len(propertyCond) {
		// get propertyID and value of fields to be shown by property condition
		objIDs, propertyIDs, showFields.propertyIDMapShowFields, err = lgc.getPropertyIDsAndShowFields(pheader, propertyCond)
		if nil != err {
			blog.Errorf("[NetProperty] search net property, get property fail, error: %v, condition [%#v]", err, propertyCond)
			return nil, err
		}

		// if find any propertyIDs matched condition, will must not find any property propetry
		if 0 == len(propertyIDs) || 0 == len(objIDs) {
			return &searchResult, nil
		}

		// propertyIDs as filter conditoin of net property
		netPropertyCond[common.BKPropertyIDField] = map[string]interface{}{common.BKDBIN: propertyIDs}
		objectCond[common.BKObjIDField] = map[string]interface{}{common.BKDBIN: objIDs}
	}

	// if obj has filter condition
	if 0 < len(objectCond) {
		// get objID and value of fields to be shown by obj condition
		objIDs, objIDMapShowFields, err = lgc.getObjIDsAndShowFields(pheader, objectCond)
		if nil != err {
			blog.Errorf("[NetProperty] search net property, get net object fail, error: %v, condition [%#v]", err, objectCond)
			return nil, err
		}

		// if not find any objID matched condition, will not find any device propetry
		if 0 == len(objIDs) {
			return &searchResult, nil
		}

		// if could get object from object condition, condition of device and property will not empty
		// objIDs as filter condition of device and property
		deviceCond[common.BKObjIDField] = map[string]interface{}{common.BKDBIN: objIDs}
		propertyCond[common.BKObjIDField] = map[string]interface{}{common.BKDBIN: objIDs}
	}

	// if device has filter condition
	if 0 < len(deviceCond) {
		if 0 == len(objIDMapShowFields) {
			_, objIDMapShowFields, err = lgc.getObjIDsAndShowFields(pheader, map[string]interface{}{})
			if nil != err {
				return nil, err
			}
			if 0 == len(objIDMapShowFields) {
				blog.Errorf("[NetProperty] search net object failed, could not get any net object")
				return nil, defErr.Errorf(common.CCErrCollectNetPropertyGetFail)
			}
		}

		// get deviceID and value of fields to be shown by device condition
		deviceIDs, showFields.deviceIDMapDeviceShowFields, err = lgc.getDeviceIDsAndShowFields(
			pheader, deviceCond, objIDMapShowFields)
		if nil != err {
			blog.Errorf("[NetProperty] search net property, get net device fail, error: %v, condition [%#v]", err, deviceCond)
			return nil, err
		}

		// if find any deviceIDs matched condition, will must not find any device propetry
		if 0 == len(deviceIDs) {
			return &searchResult, nil
		}

		// deviceIDs as filter conditoin of net property
		netPropertyCond[common.BKDeviceIDField] = map[string]interface{}{common.BKDBIN: deviceIDs}
	}

	netPropertyCond[common.BKOwnerIDField] = util.GetOwnerID(pheader)
	searchResult.Count, err = lgc.Instance.GetCntByCondition(common.BKTableNameNetcollectProperty, netPropertyCond)
	if nil != err {
		blog.Errorf("[NetProperty] search net property fail, count net property by condition [%#v] error: %v", propertyCond, err)
		return nil, err
	}
	if 0 == searchResult.Count {
		return &searchResult, nil
	}

	// field device_id and bk_property_id must be in params.Fields
	// to help add value of fields from other tables into search result
	if 0 != len(params.Fields) {
		params.Fields = append(params.Fields, []string{common.BKDeviceIDField, common.BKPropertyIDField}...)
	}

	if err = lgc.findProperty(params.Fields, netPropertyCond, &searchResult.Info, params.Page.Sort, params.Page.Start, params.Page.Limit); nil != err {
		blog.Errorf("[NetProperty] search net property fail, search net property by condition [%#v] error: %v", propertyCond, err)
		return nil, defErr.Errorf(common.CCErrCollectNetPropertyGetFail)
	}

	// if net property are not empty, should add property and device shown info to the net property result
	deviceShowFieldLen := len(showFields.deviceIDMapDeviceShowFields)
	propertyShowFieldLen := len(showFields.propertyIDMapShowFields)

	// if object condition cond and device condition is empty, device shown fields will be empty
	// if property condition is empty, property shown fields will be empty
	if 0 == deviceShowFieldLen || 0 == propertyShowFieldLen {
		deviceIDs, propertyIDs = lgc.getDeviceIDsAndPropertyIDsFromNetPropertys(&(searchResult.Info))
	}

	if 0 == deviceShowFieldLen {
		showFields.deviceIDMapDeviceShowFields, err = lgc.getDeviceShowField(pheader, deviceIDs)
		if nil != err {
			blog.Errorf("[NetProperty] search net property, get device show info fail, error: %v", err)
			return nil, defErr.Errorf(common.CCErrCollectNetPropertyGetFail)
		}
	}
	if 0 == propertyShowFieldLen {
		showFields.propertyIDMapShowFields, err = lgc.getPropertyShowField(pheader, propertyIDs)
		if nil != err {
			blog.Errorf("[NetProperty] search net property, get device show info fail, error: %v", err)
			return nil, defErr.Errorf(common.CCErrCollectNetPropertyGetFail)
		}
	}

	// add value of fields from other tables into search result
	lgc.addShowFieldValueIntoNetProperty(&(searchResult.Info), showFields)

	return &searchResult, nil
}

func (lgc *Logics) DeleteProperty(pheader http.Header, netPropertyID int64) error {
	defErr := lgc.Engine.CCErr.CreateDefaultCCErrorIf(util.GetLanguage(pheader))

	netPropertyCond := map[string]interface{}{
		common.BKOwnerIDField:               util.GetOwnerID(pheader),
		common.BKNetcollectPropertyIDlField: netPropertyID}

	rowCount, err := lgc.Instance.GetCntByCondition(common.BKTableNameNetcollectProperty, netPropertyCond)
	if nil != err {
		blog.Errorf("delete net property with id [%d], but query failed, err: %v, params: %#v", netPropertyID, err, netPropertyCond)
		return defErr.Error(common.CCErrCollectNetPropertyDeleteFail)
	}

	if 0 == rowCount {
		blog.Errorf("delete net property with id [%d] fail, but net property not exists, params: %#v", netPropertyID, netPropertyCond)
		return defErr.Error(common.CCErrCollectNetDeviceObjPropertyNotExist)
	}

	if 1 < rowCount {
		blog.Errorf("delete net property fail, there are [%d] net propertys with same id [%d], params: %#v", rowCount, netPropertyID, netPropertyCond)
		return defErr.Error(common.CCErrCollectNetPropertyDeleteFail)
	}

	if err = lgc.Instance.DelByCondition(common.BKTableNameNetcollectProperty, netPropertyCond); nil != err {
		blog.Errorf("delete net property with id [%d] failed, err: %v, params: %#v", netPropertyID, err, netPropertyCond)
		return defErr.Error(common.CCErrCollectNetPropertyDeleteFail)
	}

	blog.V(4).Infof("delete net property with id [%d] success", netPropertyID)

	return nil
}

func (lgc *Logics) addProperty(pheader http.Header, netPropertyInfo meta.NetcollectProperty, ownerID string) (int64, error) {
	defErr := lgc.Engine.CCErr.CreateDefaultCCErrorIf(util.GetLanguage(pheader))

	if "" == netPropertyInfo.OID { // check oid
		blog.Errorf("[NetProperty] add net collect property fail, oid is empty")
		return -1, defErr.Errorf(common.CCErrCommParamsLostField, common.BKOIDField)
	}

	// check period
	var err error
	if "" != netPropertyInfo.Period && common.Infinite != netPropertyInfo.Period {
		netPropertyInfo.Period, err = util.FormatPeriod(netPropertyInfo.Period)
		if nil != err {
			blog.Errorf("[NetProperty] add net collect property, format period [%s] fail, error: %v", err)
			return -1, defErr.Errorf(common.CCErrCollectPeriodFormatFail)
		}
	}

	// check action
	if "" != netPropertyInfo.Action && !lgc.isValidAction(netPropertyInfo.Action) {
		blog.Errorf("[NetProperty] add net collect property, check action fail, action [%s] must be 'get' or 'walk'")
		return -1, defErr.Errorf(common.CCErrCommParamsInvalid, common.BKActionField)
	}

	// check device
	if err = lgc.checkIfNetDeviceExist(pheader, &netPropertyInfo); nil != err {
		blog.Errorf("[NetProperty] add net collect property, check device fail, error: %v", err)
		return -1, err
	}

	// check property
	if err = lgc.checkIfNetProperty(pheader, &netPropertyInfo); nil != err {
		blog.Errorf("[NetProperty] add net collect property, check property fail, error: %v", err)
		return -1, err
	}

	// check if data duplication
	isExist, err := lgc.checkNetPropertyExist(netPropertyInfo.DeviceID, netPropertyInfo.PropertyID, ownerID)
	if nil != err {
		blog.Errorf("[NetProperty] add net collect property, check data duplication fail, error: %v", err)
		return -1, defErr.Errorf(common.CCErrCollectNetPropertyCreateFail)
	}
	if isExist {
		blog.Errorf("[NetProperty] add net collect property fail, error: duplicate [deviceID+propertyID]")
		return -1, defErr.Errorf(common.CCErrCommDuplicateItem)
	}

	now := time.Now()
	netPropertyInfo.LastTime = &now
	netPropertyInfo.OwnerID = ownerID

	if !isExist {
		netPropertyInfo.CreateTime = &now

		// set default value
		if "" == netPropertyInfo.Action {
			netPropertyInfo.Action = common.SNMPActionGet
		}
		if "" == netPropertyInfo.Period {
			netPropertyInfo.Period = common.Infinite
		}

		netPropertyInfo.NetcollectPropertyID, err = lgc.Instance.GetIncID(common.BKTableNameNetcollectProperty)
		if nil != err {
			blog.Errorf("add net collect property, failed to get id, error: %v", err)
			return -1, defErr.Errorf(common.CCErrCollectNetPropertyCreateFail)
		}

		if _, err = lgc.Instance.Insert(common.BKTableNameNetcollectProperty, netPropertyInfo); nil != err {
			blog.Errorf("failed to insert net collect property, error: %v", err)
			return -1, defErr.Errorf(common.CCErrCollectNetPropertyCreateFail)
		}

		blog.V(4).Infof("add net property by netPropertyInfo [%#+v]", netPropertyInfo)

		return netPropertyInfo.NetcollectPropertyID, nil
	}

	netPropertyID, err := lgc.getNetPropertyID(netPropertyInfo.PropertyID, netPropertyInfo.DeviceID, ownerID)
	if nil != err {
		blog.Errorf("update net proeprty, failed to get id, error: %v", err)
		return -1, defErr.Errorf(common.CCErrCollectNetPropertyCreateFail)
	}

	if err = lgc.updateNetProperty(netPropertyInfo); nil != err {
		blog.Errorf("update net proeprty failed, error: %v", err)
		return -1, err
	}

	blog.V(4).Infof("update net proeprty netPropertyInfo [%#+v]", netPropertyInfo)

	return netPropertyID, nil
}

func (lgc *Logics) getNetPropertyID(propertyID string, deviceID int64, ownerID string) (int64, error) {
	queryParams := common.KvMap{
		common.BKDeviceIDField: deviceID, common.BKPropertyIDField: propertyID, common.BKOwnerIDField: ownerID}

	result := meta.NetcollectProperty{}

	if err := lgc.Instance.GetOneByCondition(common.BKTableNameNetcollectProperty, nil, queryParams, &result); nil != err {
		blog.Errorf(
			"get net property ID by propertyID and deviceID, query device fail, error information is %v, params:%v",
			err, queryParams)
		return 0, err
	}

	return result.NetcollectPropertyID, nil
}

func (lgc *Logics) updateNetProperty(netPropertyInfo meta.NetcollectProperty) error {
	queryParams := common.KvMap{
		common.BKDeviceIDField:   netPropertyInfo.DeviceID,
		common.BKPropertyIDField: netPropertyInfo.PropertyID,
		common.BKOwnerIDField:    netPropertyInfo.OwnerID}

	if err := lgc.Instance.UpdateByCondition(common.BKTableNameNetcollectProperty, netPropertyInfo, queryParams); nil != err {
		blog.Errorf("update net property fail, error information is %v, params:%v", err, queryParams)
		return err
	}

	return nil
}

// check if bk_property_id is valid and from object of net device
// if bk_property_id is valid, propertyInfo will get bk_property_id of property
func (lgc *Logics) checkIfNetProperty(pheader http.Header, propertyInfo *meta.NetcollectProperty) error {
	var err error
	propertyInfo.PropertyID, err = lgc.checkNetObjectProperty(pheader, propertyInfo.ObjectID, propertyInfo.PropertyID, propertyInfo.PropertyName)
	return err
}

// check if device exist or not
// if device exist, propertyInfo will get bk_device_id of device
func (lgc *Logics) checkIfNetDeviceExist(pheader http.Header, propertyInfo *meta.NetcollectProperty) error {
	var err error
	propertyInfo.DeviceID, propertyInfo.ObjectID, err = lgc.checkNetDeviceExist(pheader, propertyInfo.DeviceID, propertyInfo.DeviceName)
	return err
}

// check if there is the same propertyInfo
func (lgc *Logics) checkNetPropertyExist(deviceID int64, propertyID, ownerID string) (bool, error) {
	queryParams := common.KvMap{
		common.BKDeviceIDField: deviceID, common.BKPropertyIDField: propertyID, common.BKOwnerIDField: ownerID}

	rowCount, err := lgc.Instance.GetCntByCondition(common.BKTableNameNetcollectProperty, queryParams)
	if nil != err {
		blog.Errorf("[NetProperty] check if net deviceID and propertyID exist, query device fail, error information is %v, params:%v",
			err, queryParams)
		return false, err
	}

	if 0 != rowCount {
		blog.V(4).Infof(
			"[NetProperty] check if net deviceID and propertyID exist, bk_device_id is [%s] bk_property_id [%s] device is exist",
			deviceID, propertyID)
		return true, nil
	}

	return false, nil
}

func (lgc *Logics) isValidAction(action string) bool {
	return common.SNMPActionGet == action || common.SNMPActionWalk == action
}

func (lgc *Logics) findProperty(fields []string, condition, result interface{}, sort string, skip, limit int) error {
	if err := lgc.Instance.GetMutilByCondition(common.BKTableNameNetcollectProperty, fields, condition, result, sort, skip, limit); err != nil {
		blog.Errorf("[NetProperty] failed to query the inst, error info %s", err.Error())
		return err
	}

	return nil
}

func (lgc *Logics) classifyNetPropertyCondition(
	conditionList []meta.ConditionItem) (map[string]interface{}, map[string]interface{}, map[string]interface{}, map[string]interface{}) {

	deviceCond := map[string]interface{}{}
	objectCond := map[string]interface{}{}
	propertyCond := map[string]interface{}{}
	netPropertyCond := map[string]interface{}{}

	for _, cond := range conditionList {
		if cond.Operator == common.BKDBEQ {
			switch cond.Field {
			case meta.AttributeFieldUnit, common.BKPropertyNameField, common.BKPropertyIDField:
				propertyCond[cond.Field] = cond.Value
			case common.BKObjIDField, common.BKObjNameField:
				objectCond[cond.Field] = cond.Value
			case common.BKDeviceIDField, common.BKDeviceNameField, common.BKDeviceModelField:
				deviceCond[cond.Field] = cond.Value
			default:
				netPropertyCond[cond.Field] = cond.Value
			}
		} else {
			switch cond.Field {
			case meta.AttributeFieldUnit, common.BKPropertyNameField, common.BKPropertyIDField:
				propertyCond[cond.Field] = map[string]interface{}{cond.Operator: cond.Value}
			case common.BKObjIDField, common.BKObjNameField:
				objectCond[cond.Field] = map[string]interface{}{cond.Operator: cond.Value}
			case common.BKDeviceIDField, common.BKDeviceNameField, common.BKDeviceModelField:
				deviceCond[cond.Field] = map[string]interface{}{cond.Operator: cond.Value}
			default:
				netPropertyCond[cond.Field] = map[string]interface{}{cond.Operator: cond.Value}
			}
		}
	}

	return deviceCond, objectCond, propertyCond, netPropertyCond
}

type netPropertyShowFields struct {
	deviceIDMapDeviceShowFields map[int64]deviceShowField    // id map value group of device fields
	propertyIDMapShowFields     map[string]propertyShowField // propertyID+objID map value group of property fields
}

type objShowField struct {
	objName string
}

type deviceShowField struct {
	deviceName  string
	deviceModel string
	objID       string
	objName     string
}

type propertyShowField struct {
	unit         string
	propertyName string
}

// get obj ID list and get field to show by map (bk_obj_id --> bk_obj_name)
func (lgc *Logics) getObjIDsAndShowFields(pheader http.Header, objectCond map[string]interface{}) ([]string, map[string]objShowField, error) {
	defErr := lgc.Engine.CCErr.CreateDefaultCCErrorIf(util.GetLanguage(pheader))
	objectCond[common.BKClassificationIDField] = common.BKNetwork

	objResult, err := lgc.CoreAPI.ObjectController().Meta().SelectObjects(context.Background(), pheader, objectCond)
	if nil != err {
		blog.Errorf("[NetProperty] get net device object fail, error: %v, condition [%#v]", err, objectCond)
		return nil, nil, defErr.Errorf(common.CCErrObjectSelectInstFailed)
	}
	if !objResult.Result {
		blog.Errorf("[NetProperty] get net device object fail, errors: %s, condition [%#v]", objResult.ErrMsg, objectCond)
		return nil, nil, defErr.Errorf(objResult.Code)
	}

	if nil == objResult.Data || 0 == len(objResult.Data) {
		return nil, nil, nil
	}

	objIDs := []string{}
	objIDMapobjName := map[string]objShowField{}
	for _, obj := range objResult.Data {
		objIDs = append(objIDs, obj.ObjectID)
		objIDMapobjName[obj.ObjectID] = objShowField{obj.ObjectName}
	}

	return objIDs, objIDMapobjName, nil
}

// get device ID list and get field to show by map (bk_device_id --> bk_device_name, ...)
// add obj show field into device show fields
func (lgc *Logics) getDeviceIDsAndShowFields(
	pheader http.Header, deviceCond map[string]interface{}, objIDMapShowFields map[string]objShowField) ([]int64, map[int64]deviceShowField, error) {

	defErr := lgc.Engine.CCErr.CreateDefaultCCErrorIf(util.GetLanguage(pheader))

	deviceCond[common.BKOwnerIDField] = util.GetOwnerID(pheader)
	deviceField := []string{common.BKDeviceIDField, common.BKDeviceNameField, common.BKDeviceModelField, common.BKObjIDField}
	deviceResult := []meta.NetcollectDevice{}

	if err := lgc.findDevice(deviceField, deviceCond, &deviceResult, "", 0, 0); nil != err {
		blog.Errorf("[NetProperty] search net device fail by condition [%#v], error: %v", deviceCond, err)
		if mgo.ErrNotFound == err {
			return nil, nil, nil
		}
		return nil, nil, defErr.Errorf(common.CCErrCollectNetDeviceGetFail)
	}

	deviceIDs, deviceIDMapDeviceShowFields := lgc.assembleDeviceShowFieldValue(&deviceResult, objIDMapShowFields)

	if 0 == len(deviceIDs) {
		return nil, nil, nil
	}

	return deviceIDs, deviceIDMapDeviceShowFields, nil
}

// get device IDs from device list
// assemble value of device list: [deviceID] map [deviceName, deviceModel, objID, objName]
// objName is taken from objIDMapShowFields
func (lgc *Logics) assembleDeviceShowFieldValue(deviceData *[]meta.NetcollectDevice, objIDMapShowFields map[string]objShowField) (
	deviceIDs []int64, deviceIDMapDeviceShowFields map[int64]deviceShowField) {

	if nil == deviceData || 0 == len(*deviceData) {
		return deviceIDs, deviceIDMapDeviceShowFields
	}

	deviceIDMapDeviceShowFields = map[int64]deviceShowField{}

	for _, device := range *deviceData {
		// get device IDs from device list
		deviceIDs = append(deviceIDs, device.DeviceID)
		// assemble value of device list: [deviceID] map [deviceName, deviceModel, objID, objName]
		deviceIDMapDeviceShowFields[device.DeviceID] = deviceShowField{
			device.DeviceName, device.DeviceModel, device.ObjectID, objIDMapShowFields[device.ObjectID].objName,
		}
	}

	return deviceIDs, deviceIDMapDeviceShowFields
}

// get objectID, property ID list and get field to show by map (bk_property_id --> bk_property_name, ...)
func (lgc *Logics) getPropertyIDsAndShowFields(
	pheader http.Header, propertyCond map[string]interface{}) ([]string, []string, map[string]propertyShowField, error) {

	defErr := lgc.Engine.CCErr.CreateDefaultCCErrorIf(util.GetLanguage(pheader))

	attrResult, err := lgc.CoreAPI.ObjectController().Meta().SelectObjectAttWithParams(
		context.Background(), pheader, propertyCond)
	if nil != err {
		blog.Errorf("[NetProperty] get property fail, error: %v, condition [%#v]", err, propertyCond)
		return nil, nil, nil, defErr.Errorf(common.CCErrTopoObjectAttributeSelectFailed)
	}
	if !attrResult.Result {
		blog.Errorf("[NetProperty] get property fail, error: %s", attrResult.ErrMsg)
		return nil, nil, nil, defErr.Errorf(attrResult.Code)
	}

	objIDs, propertyIDs, propertyIDMapPropertyShowFields := lgc.assembleAttrShowFieldValue(&(attrResult.Data))

	if 0 == len(objIDs) || 0 == len(propertyIDs) || 0 == len(propertyIDMapPropertyShowFields) {
		blog.Errorf("[NetProperty] get property fail, property is not exist, condition [%#v]", propertyCond)
		return nil, nil, nil, nil
	}

	return objIDs, propertyIDs, propertyIDMapPropertyShowFields, nil
}

// get obj IDs and property IDs , assemble value of attribute list:[propertyID + objID] map [property show fields]
func (lgc *Logics) assembleAttrShowFieldValue(attrData *[]meta.Attribute) (
	objIDs []string, propertyIDs []string, propertyIDMapPropertyShowFields map[string]propertyShowField) {

	if nil == attrData || 0 == len(*attrData) {
		return []string{}, []string{}, map[string]propertyShowField{}
	}

	// get obj IDs and property IDs from attribute list
	propertyIDs, objIDs = []string{}, []string{}
	// assemble value of attribute list: [propertyID + objID] map [property unit, property name]
	propertyIDMapPropertyShowFields = map[string]propertyShowField{}

	for _, property := range *attrData {
		propertyIDs = append(propertyIDs, property.PropertyID)
		objIDs = append(objIDs, property.ObjectID)

		propertyIDMapPropertyShowFields[property.PropertyID+property.ObjectID] = propertyShowField{
			property.Unit,
			property.PropertyName,
		}
	}

	return objIDs, propertyIDs, propertyIDMapPropertyShowFields
}

// add group value of device and property to net property
func (lgc *Logics) addShowFieldValueIntoNetProperty(
	netProperty *[]meta.NetcollectProperty, netPropShowFields netPropertyShowFields) {

	for index := range *netProperty {

		deviceValue := netPropShowFields.deviceIDMapDeviceShowFields[(*netProperty)[index].DeviceID]

		// add group value of device
		(*netProperty)[index].DeviceModel = deviceValue.deviceModel
		(*netProperty)[index].DeviceName = deviceValue.deviceName
		(*netProperty)[index].ObjectID = deviceValue.objID
		(*netProperty)[index].ObjectName = deviceValue.objName

		propertyID := (*netProperty)[index].PropertyID
		propertyValue := netPropShowFields.propertyIDMapShowFields[propertyID+deviceValue.objID]
		// add group value of property
		(*netProperty)[index].Unit = propertyValue.unit
		(*netProperty)[index].PropertyName = propertyValue.propertyName
	}
}

func (lgc *Logics) getDeviceIDsAndPropertyIDsFromNetPropertys(
	netProperty *[]meta.NetcollectProperty) (deviceIDs []int64, propertyIDs []string) {

	for index := range *netProperty {
		deviceIDs = append(deviceIDs, (*netProperty)[index].DeviceID)
		propertyIDs = append(propertyIDs, (*netProperty)[index].PropertyID)
	}

	return deviceIDs, propertyIDs
}

// get device shown info by deviceIDs
func (lgc *Logics) getDeviceShowField(pheader http.Header, deviceIDs []int64) (map[int64]deviceShowField, error) {
	_, objIDMapShowFields, err := lgc.getObjIDsAndShowFields(pheader, map[string]interface{}{})
	if nil != err {
		return nil, err
	}
	if 0 == len(objIDMapShowFields) {
		return nil, fmt.Errorf("search net object failed, could not get any net object")
	}

	deviceCond := map[string]interface{}{
		common.BKDeviceIDField: map[string]interface{}{common.BKDBIN: deviceIDs},
		common.BKOwnerIDField:  util.GetOwnerID(pheader),
	}
	_, deviceIDMapDeviceShowFields, err := lgc.getDeviceIDsAndShowFields(pheader, deviceCond, objIDMapShowFields)
	if nil != err {
		return nil, err
	}

	if 0 == len(deviceIDMapDeviceShowFields) {
		return nil, fmt.Errorf("search net device failed, could not get any net device by condition [%#+v]", deviceCond)
	}

	return deviceIDMapDeviceShowFields, nil
}

// get property shown info by propertyIDs
func (lgc *Logics) getPropertyShowField(pheader http.Header, propertyIDs []string) (map[string]propertyShowField, error) {
	propertyCond := map[string]interface{}{
		common.BKPropertyIDField: map[string]interface{}{common.BKDBIN: propertyIDs},
	}

	_, _, propertyIDMapPropertyShowFields, err := lgc.getPropertyIDsAndShowFields(pheader, propertyCond)
	if nil != err {
		return nil, err
	}

	if 0 == len(propertyIDMapPropertyShowFields) {
		return nil, fmt.Errorf("search property failed, could not get any property by condition [%#+v]", propertyCond)
	}

	return propertyIDMapPropertyShowFields, nil
}
