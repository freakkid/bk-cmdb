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
	"net/http"
	"time"

	"configcenter/src/common"
	"configcenter/src/common/blog"
	meta "configcenter/src/common/metadata"
	"configcenter/src/common/util"
)

func (lgc *Logics) AddDevices(pheader http.Header, deviceInfoList []meta.NetcollectDevice) ([]meta.AddDeviceResult, bool) {
	ownerID := util.GetOwnerID(pheader)

	resultList := make([]meta.AddDeviceResult, 0)
	hasError := false

	for _, deviceInfo := range deviceInfoList {
		errMsg := ""
		result := true

		deviceID, err := lgc.addDevice(deviceInfo, pheader, ownerID)
		if nil != err {
			errMsg = err.Error()
			result = false
			hasError = true
		}

		resultList = append(resultList, meta.AddDeviceResult{result, errMsg, deviceID})
	}

	return resultList, hasError
}

func (lgc *Logics) SearchDevice(pheader http.Header, params *meta.NetCollSearchParams) (meta.SearchNetDevice, error) {
	defErr := lgc.Engine.CCErr.CreateDefaultCCErrorIf(util.GetLanguage(pheader))

	deviceCond := map[string]interface{}{}
	deviceCond[common.BKOwnerIDField] = util.GetOwnerID(pheader)

	objCond := map[string]interface{}{}

	// get condition, condtion of objs and condtion of device
	for _, cond := range params.Condition {
		switch cond.Operator {
		case common.BKDBEQ:
			if common.BKObjNameField == cond.Field {
				objCond[cond.Field] = cond.Value
			} else {
				deviceCond[cond.Field] = cond.Value
			}
		default:
			if common.BKObjNameField == cond.Field {
				objCond[cond.Field] = map[string]interface{}{
					cond.Operator: cond.Value,
				}
			} else {
				deviceCond[cond.Field] = map[string]interface{}{
					cond.Operator: cond.Value,
				}
			}
		}
	}

	// if condition only has bk_obj_name but not bk_obj_id
	// get net device bk_obj_id from bk_obj_name
	if _, ok := deviceCond[common.BKObjIDField]; !ok && 0 < len(objCond) {
		objIDs, err := lgc.getNetDeviceObjIDsByCond(objCond, pheader)
		if nil != err {
			blog.Errorf("[NetDevice] search net device fail, search net device obj id by condition [%#v] error: %v", objCond, err)
			return meta.SearchNetDevice{}, defErr.Errorf(common.CCErrCollectNetDeviceGetFail)
		}
		deviceCond[common.BKObjIDField] = map[string]interface{}{
			common.BKDBIN: objIDs,
		}
	}

	searchResult := meta.SearchNetDevice{}
	var err error

	searchResult.Count, err = lgc.Instance.GetCntByCondition(common.BKTableNameNetcollectDevice, deviceCond)
	if nil != err {
		blog.Errorf("[NetDevice] search net device fail, count net device by condition [%#v] error: %v", deviceCond, err)
		return meta.SearchNetDevice{}, err
	}
	if 0 == searchResult.Count {
		searchResult.Info = []meta.NetcollectDevice{}
		return searchResult, nil
	}

	// field bk_obj_id must be in params.Fields//TODO
	// to help add value of fields(bk_obj_name) from other tables into search result
	if 0 != len(params.Fields) {
		params.Fields = append(params.Fields, []string{common.BKObjIDField}...)
	}
	if err = lgc.findDevice(params.Fields, deviceCond, &searchResult.Info, params.Page.Sort, params.Page.Start, params.Page.Limit); nil != err {
		blog.Errorf("[NetDevice] search net device fail, search net device by condition [%#v] error: %v", deviceCond, err)
		return meta.SearchNetDevice{}, defErr.Errorf(common.CCErrCollectNetDeviceGetFail)
	}

	//TODO 增加 obj_name

	return searchResult, nil
}

func (lgc *Logics) DeleteDevice(pheader http.Header, netDeviceID int64) error {
	defErr := lgc.Engine.CCErr.CreateDefaultCCErrorIf(util.GetLanguage(pheader))
	ownerID := util.GetOwnerID(pheader)

	deviceCond := map[string]interface{}{
		common.BKOwnerIDField:  ownerID,
		common.BKDeviceIDField: netDeviceID}

	rowCount, err := lgc.Instance.GetCntByCondition(common.BKTableNameNetcollectDevice, deviceCond)
	if nil != err {
		blog.Errorf("[NetDevice] delete net device with id [%d], but query failed, err: %v, params: %#v", netDeviceID, err, deviceCond)
		return defErr.Error(common.CCErrCollectNetDeviceDeleteFail)
	}

	if 0 == rowCount {
		blog.Errorf("[NetDevice] delete net device with id [%d] fail, but device not exists, params: %#v", netDeviceID, deviceCond)
		return defErr.Error(common.CCErrCollectDeviceNotExist)
	}

	if 1 < rowCount {
		blog.Errorf("[NetDevice] delete net device with id [%d] fail, there are [%d] devices with same deviceID", netDeviceID, rowCount)
		return defErr.Error(common.CCErrCollectNetDeviceDeleteFail)
	}

	// check if net device has property
	hasProperty, err := lgc.checkDeviceHasProperty(netDeviceID, ownerID)
	if nil != err {
		return defErr.Error(common.CCErrCollectNetDeviceDeleteFail)
	}
	if hasProperty {
		blog.Errorf("[NetDevice] delete net device fail, net device has property [%d]", netDeviceID)
		return defErr.Error(common.CCErrCollectNetDeviceHasPropertyDeleteFail)
	}

	if err = lgc.Instance.DelByCondition(common.BKTableNameNetcollectDevice, deviceCond); nil != err {
		blog.Errorf("[NetDevice] delete net device with id [%d] failed, err: %v, params: %#v", netDeviceID, err, deviceCond)
		return defErr.Error(common.CCErrCollectNetDeviceDeleteFail)
	}

	blog.V(4).Infof("[NetDevice] delete net device with id [%d] success", netDeviceID)
	return nil
}

// add a device
func (lgc *Logics) addDevice(deviceInfo meta.NetcollectDevice, pheader http.Header, ownerID string) (int64, error) {
	defErr := lgc.Engine.CCErr.CreateDefaultCCErrorIf(util.GetLanguage(pheader))
	if "" == deviceInfo.DeviceModel {
		blog.Errorf("[NetDevice] add net device fail, device_model is empty")
		return -1, defErr.Errorf(common.CCErrCommParamsLostField, common.BKDeviceModelField)
	}

	if "" == deviceInfo.BkVendor {
		blog.Errorf("[NetDevice] add net device fail, bk_vendor is empty")
		return -1, defErr.Errorf(common.CCErrCommParamsLostField, common.BKVendorField)
	}

	if "" == deviceInfo.DeviceName {
		blog.Errorf("[NetDevice] add net device fail, device_name is empty")
		return -1, defErr.Errorf(common.CCErrCommParamsLostField, common.BKDeviceModelField)
	}

	// check if bk_object_id and bk_object_name are net device object
	err := lgc.checkIfNetDeviceObject(&deviceInfo, pheader)
	if nil != err {
		blog.Errorf("[NetDevice] add net device fail, error: %v, object name [%s] and object ID [%s]",
			err, deviceInfo.ObjectName, deviceInfo.ObjectID)
		return -1, err
	}

	// check if device_name exist
	isExist, err := lgc.checkIfNetDeviceNameExist(deviceInfo.DeviceName, ownerID)
	if nil != err {
		blog.Errorf("[NetDevice] add net device fail, error: %v", err)
		return -1, defErr.Errorf(common.CCErrCollectNetDeviceCreateFail)
	}

	// add to the storage
	now := time.Now()
	deviceInfo.LastTime = &now
	deviceInfo.OwnerID = ownerID

	if !isExist {
		deviceInfo.CreateTime = &now

		deviceInfo.DeviceID, err = lgc.Instance.GetIncID(common.BKTableNameNetcollectDevice)
		if nil != err {
			blog.Errorf("[NetDevice] add net device, failed to get id, error: %v", err)
			return -1, defErr.Errorf(common.CCErrCollectNetDeviceCreateFail)
		}

		if _, err = lgc.Instance.Insert(common.BKTableNameNetcollectDevice, deviceInfo); nil != err {
			blog.Errorf("[NetDevice] failed to insert net device, error: %v", err)
			return -1, defErr.Errorf(common.CCErrCollectNetDeviceCreateFail)
		}

		blog.V(4).Infof("[NetDevice] add net device by deviceInfo [%#+v]", deviceInfo)

		return deviceInfo.DeviceID, nil
	}

	deviceID, err := lgc.getNetDeviceIDByName(deviceInfo.DeviceName, ownerID)
	if nil != err {
		blog.Errorf("[NetDevice] add net device, failed to get id, error: %v", err)
		return -1, defErr.Errorf(common.CCErrCollectNetDeviceCreateFail)
	}

	if err = lgc.updateNetDeviceByName(deviceInfo); nil != err {
		blog.Errorf("[NetDevice] update net device failed, error: %v", err)
		return -1, err
	}

	blog.V(4).Infof("[NetDevice] update net device by name[%s] deviceInfo [%#+v]", deviceInfo.DeviceName, deviceInfo)

	return deviceID, nil
}

func (lgc *Logics) findDevice(fields []string, condition, result interface{}, sort string, skip, limit int) error {
	if err := lgc.Instance.GetMutilByCondition(common.BKTableNameNetcollectDevice, fields, condition, result, sort, skip, limit); err != nil {
		blog.Errorf("[NetDevice] failed to query the inst, error info %s", err.Error())
		return err
	}

	return nil
}

// check the deviceInfo if is a net object
// by checking if bk_obj_id and bk_obj_name function parameter are valid net device object or not
func (lgc *Logics) checkIfNetDeviceObject(deviceInfo *meta.NetcollectDevice, pheader http.Header) error {
	var err error
	deviceInfo.ObjectID, deviceInfo.ObjectName, err = lgc.checkNetObject(deviceInfo.ObjectID, deviceInfo.ObjectName, pheader)
	return err
}

// check if net device name exist
func (lgc *Logics) checkIfNetDeviceNameExist(deviceName string, ownerID string) (bool, error) {
	queryParams := common.KvMap{common.BKDeviceNameField: deviceName, common.BKOwnerIDField: ownerID}

	rowCount, err := lgc.Instance.GetCntByCondition(common.BKTableNameNetcollectDevice, queryParams)
	if nil != err {
		blog.Errorf("[NetDevice] check if net device name exist, query device fail, error information is %v, params:%v",
			err, queryParams)
		return false, err
	}

	if 0 != rowCount {
		blog.V(4).Infof("[NetDevice] check if net device name exist, bk_device_name is [%s] device is exist", deviceName)
		return true, nil
	}

	return false, nil
}

// check if net device name exist
func (lgc *Logics) getNetDeviceIDByName(deviceName string, ownerID string) (int64, error) {
	queryParams := common.KvMap{common.BKDeviceNameField: deviceName, common.BKOwnerIDField: ownerID}

	result := meta.NetcollectDevice{}

	if err := lgc.Instance.GetOneByCondition(common.BKTableNameNetcollectDevice, nil, queryParams, &result); nil != err {
		blog.Errorf("[NetDevice] get net device ID by name, query device fail, error information is %v, params:%v",
			err, queryParams)
		return 0, err
	}

	return result.DeviceID, nil
}

func (lgc *Logics) updateNetDeviceByName(deviceInfo meta.NetcollectDevice) error {
	queryParams := common.KvMap{
		common.BKDeviceNameField: deviceInfo.DeviceName,
		common.BKOwnerIDField:    deviceInfo.OwnerID}

	if err := lgc.Instance.UpdateByCondition(common.BKTableNameNetcollectDevice, deviceInfo, queryParams); nil != err {
		blog.Errorf("[NetDevice] update net device by name fail, error information is %v, params:%v",
			err, queryParams)
		return err
	}

	return nil
}

// get net device obj ID
func (lgc *Logics) getNetDeviceObjIDsByCond(objCond map[string]interface{}, pheader http.Header) ([]string, error) {
	defErr := lgc.Engine.CCErr.CreateDefaultCCErrorIf(util.GetLanguage(pheader))

	objIDs := []string{}

	if _, ok := objCond[common.BKObjNameField]; ok {
		objCond[common.BKClassificationIDField] = common.BKNetwork
		objResult, err := lgc.CoreAPI.ObjectController().Meta().SelectObjects(context.Background(), pheader, objCond)
		if nil != err {
			blog.Errorf("[NetDevice] check net device object ID, search objectName fail, %v", err)
			return nil, err
		}

		if !objResult.Result {
			blog.Errorf("[NetDevice] check net device object ID, errors: %s", objResult.ErrMsg)
			return nil, defErr.Errorf(objResult.Code)
		}

		if nil != objResult.Data {
			for _, data := range objResult.Data {
				objIDs = append(objIDs, data.ObjectID)
			}
		}
	}

	return objIDs, nil
}

// check if device has property
func (lgc *Logics) checkDeviceHasProperty(deviceID int64, ownerID string) (bool, error) {
	queryParams := common.KvMap{
		common.BKDeviceIDField: deviceID, common.BKOwnerIDField: ownerID}

	rowCount, err := lgc.Instance.GetCntByCondition(common.BKTableNameNetcollectProperty, queryParams)
	if nil != err {
		blog.Errorf("[NetDevice] check if net deviceID and propertyID exist, query device fail, error information is %v, params:%v",
			err, queryParams)
		return false, err
	}

	return 0 != rowCount, nil
}
