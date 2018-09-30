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

package controllers

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rentiansheng/xlsx"

	"configcenter/src/common"
	"configcenter/src/common/blog"
	"configcenter/src/common/core/cc/api"
	"configcenter/src/common/core/cc/wactions"
	"configcenter/src/common/errors"
	lang "configcenter/src/common/language"
	meta "configcenter/src/common/metadata"
	"configcenter/src/common/types"
	"configcenter/src/common/util"
	"configcenter/src/web_server/application/logics"
	webCommon "configcenter/src/web_server/common"
)

func init() {
	wactions.RegisterNewAction(wactions.Action{common.HTTPCreate, "/netdevice/import", nil, ImportNetDevice})
	wactions.RegisterNewAction(wactions.Action{common.HTTPSelectPost, "/netdevice/export", nil, ExportNetDevice})
	wactions.RegisterNewAction(wactions.Action{
		common.HTTPSelectGet, "/netcollect/importtemplate/netdevice", nil, BuildDownLoadNetDeviceExcelTemplate})
}

func ImportNetDevice(c *gin.Context) {
	cc := api.NewAPIResource()
	language := logics.GetLanguageByHTTPRequest(c)
	defLang := cc.Lang.CreateDefaultCCLanguageIf(language)
	defErr := cc.Error.CreateDefaultCCErrorIf(language)
	logics.SetProxyHeader(c)

	// open uploaded file
	err, errMsg, file := openUploadedFile(c, defErr)
	if nil != err {
		blog.Errorf("[Import Net Device] open uploaded file error:%s", err.Error())
		c.String(http.StatusInternalServerError, string(errMsg))
		return
	}

	// get date from uploaded file
	apiSite, err := cc.AddrSrv.GetServer(types.CC_MODULE_APISERVER)
	if nil != err {
		blog.Errorf("[Import Net Device] get api site error:%s", err.Error())
		c.String(http.StatusInternalServerError, getReturnStr(common.CCErrWebGetAddNetDeviceResultFail,
			defErr.Errorf(common.CCErrWebGetAddNetDeviceResultFail, err.Error()).Error(), nil))
		return
	}

	err, errMsg, netDevice := getNetDevicesFromFile(c.Request.Header, defLang, defErr, file, apiSite)
	if nil != err {
		blog.Errorf("[Import Net Device] http request id:%s, error:%s", util.GetHTTPCCRequestID(c.Request.Header), err.Error())
		c.String(http.StatusInternalServerError, string(errMsg))
		return
	}

	// http request get device
	url := apiSite + fmt.Sprintf("/api/%s/netcollect/device/action/create", webCommon.API_VERSION)
	blog.Infof("[Import Net Device] add device url: %v", url)

	params := make([]interface{}, 0)
	line_numbers := []int{}
	for line, value := range netDevice {
		params = append(params, value)
		line_numbers = append(line_numbers, line)
	}
	blog.Infof("[Import Net Device] import device content: %v", params)

	reply, err := httpRequest(url, params, c.Request.Header)
	blog.Infof("[Import Net Device] import device result: %v", reply)

	if nil != err {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}

	// rebuild response body
	err, reply = rebuildDeviceReponseBody(reply, line_numbers)
	if nil != err {
		c.String(http.StatusInternalServerError, getReturnStr(common.CCErrWebGetAddNetDeviceResultFail,
			defErr.Errorf(common.CCErrWebGetAddNetDeviceResultFail).Error(), nil))
	}

	c.String(http.StatusOK, reply)
}

func ExportNetDevice(c *gin.Context) {
	cc := api.NewAPIResource()
	language := logics.GetLanguageByHTTPRequest(c)
	defLang := cc.Lang.CreateDefaultCCLanguageIf(language)
	defErr := cc.Error.CreateDefaultCCErrorIf(language)
	logics.SetProxyHeader(c)

	apiSite, err := cc.AddrSrv.GetServer(types.CC_MODULE_APISERVER)
	if nil != err {
		blog.Errorf("[Export Net Device] get api site error:%s", err.Error())
		c.String(http.StatusInternalServerError, getReturnStr(common.CCErrWebGetNetDeviceFail,
			defErr.Errorf(common.CCErrWebGetNetDeviceFail, err.Error()).Error(), nil))
		return
	}

	deviceIDstr := c.PostForm(common.BKDeviceIDField)
	deviceInfo, err := logics.GetNetDeviceData(c.Request.Header, apiSite, deviceIDstr)
	if nil != err {
		blog.Errorf("[Export Net Device] get device data error:%s", err.Error())
		msg := getReturnStr(common.CCErrWebGetHostFail, defErr.Errorf(common.CCErrWebGetHostFail, err.Error()).Error(), nil)
		c.String(http.StatusInternalServerError, msg, nil)
		return
	}

	file := xlsx.NewFile()
	sheet, err := file.AddSheet(common.BKNetDevice)
	if nil != err {
		blog.Errorf("[Export Net Device] create sheet error:%s", err.Error())
		msg := getReturnStr(common.CCErrWebCreateEXCELFail,
			defErr.Errorf(common.CCErrWebCreateEXCELFail, err.Error()).Error(), nil)
		c.String(http.StatusInternalServerError, msg, nil)
		return
	}

	fields := logics.GetNetDevicefield(defLang)
	logics.AddNetDeviceExtFields(&fields, defLang)

	if err = logics.BuildNetDeviceExcelFromData(defLang, fields, deviceInfo, sheet); nil != err {
		blog.Errorf("[Export Net Device] build net device excel data error:%s", err.Error())
		msg := getReturnStr(common.CCErrCommExcelTemplateFailed,
			defErr.Errorf(common.CCErrCommExcelTemplateFailed, common.BKNetDevice).Error(), nil)
		c.String(http.StatusInternalServerError, msg, nil)
		return
	}

	dirFileName := fmt.Sprintf("%s/export", webCommon.ResourcePath)
	if _, err = os.Stat(dirFileName); nil != err {
		os.MkdirAll(dirFileName, os.ModeDir|os.ModePerm)
	}

	fileName := fmt.Sprintf("%dnetdevice.xlsx", time.Now().UnixNano())
	dirFileName = fmt.Sprintf("%s/%s", dirFileName, fileName)

	logics.ProductExcelCommentSheet(file, defLang)

	if err = file.Save(dirFileName); nil != err {
		blog.Error("[Export Net Device] save file error:%s", err.Error())
		msg := getReturnStr(common.CCErrWebCreateEXCELFail,
			defErr.Errorf(common.CCErrCommExcelTemplateFailed, err.Error()).Error(), nil)
		c.String(http.StatusInternalServerError, msg, nil)
		return
	}

	logics.AddDownExcelHttpHeader(c, "netdevice.xlsx")
	c.File(dirFileName)

	os.Remove(dirFileName)
}

func BuildDownLoadNetDeviceExcelTemplate(c *gin.Context) {
	logics.SetProxyHeader(c)
	cc := api.NewAPIResource()

	dir := webCommon.ResourcePath + "/template/"
	if _, err := os.Stat(dir); nil != err {
		os.MkdirAll(dir, os.ModeDir|os.ModePerm)
	}

	language := logics.GetLanguageByHTTPRequest(c)
	defLang := cc.Lang.CreateDefaultCCLanguageIf(language)
	defErr := cc.Error.CreateDefaultCCErrorIf(language)

	randNum := rand.Uint32()
	file := fmt.Sprintf("%s/%stemplate-%d-%d.xlsx", dir, common.BKNetDevice, time.Now().UnixNano(), randNum)

	apiSite := cc.APIAddr()
	if err := logics.BuildNetDeviceExcelTemplate(c.Request.Header, defLang, apiSite, file); nil != err {
		blog.Errorf("Build NetDevice Excel Template fail, error:%s", err.Error())
		reply := getReturnStr(common.CCErrCommExcelTemplateFailed,
			defErr.Errorf(common.CCErrCommExcelTemplateFailed, common.BKNetDevice).Error(),
			nil)
		c.Writer.Write([]byte(reply))
		return
	}

	logics.AddDownExcelHttpHeader(c, fmt.Sprintf("template_%s.xlsx", common.BKNetDevice))

	c.File(file)
	os.Remove(file)
	return
}

func openUploadedFile(c *gin.Context, defErr errors.DefaultCCErrorIf) (err error, errMsg string, file *xlsx.File) {
	fileHeader, err := c.FormFile("file")
	if nil != err {
		errMsg = getReturnStr(common.CCErrWebFileNoFound, defErr.Error(common.CCErrWebFileNoFound).Error(), nil)
		return err, errMsg, nil
	}

	dir := webCommon.ResourcePath + "/import/"
	if _, err = os.Stat(dir); nil != err {
		os.MkdirAll(dir, os.ModeDir|os.ModePerm)
	}

	filePath := fmt.Sprintf("%s/importnetdevice-%d-%d.xlsx", dir, time.Now().UnixNano(), rand.Uint32())
	if err = c.SaveUploadedFile(fileHeader, filePath); nil != err {
		errMsg = getReturnStr(common.CCErrWebFileSaveFail, defErr.Errorf(common.CCErrWebFileSaveFail, err.Error()).Error(), nil)
		return err, errMsg, nil
	}

	defer os.Remove(filePath) // del file

	file, err = xlsx.OpenFile(filePath)
	if nil != err {
		errMsg = getReturnStr(common.CCErrWebOpenFileFail, defErr.Errorf(common.CCErrWebOpenFileFail, err.Error()).Error(), nil)
		return err, errMsg, nil
	}

	return nil, "", file
}

func getNetDevicesFromFile(
	header http.Header, defLang lang.DefaultCCLanguageIf, defErr errors.DefaultCCErrorIf, file *xlsx.File, apiSite string) (
	err error, errMsg string, netDevice map[int]map[string]interface{}) {

	netDevice, errMsgs, err := logics.GetImportNetDevices(header, defLang, file, apiSite)
	if nil != err {
		blog.Errorf("[Import Net Device] http request id:%s, error:%s", util.GetHTTPCCRequestID(header), err.Error())
		errMsg = getReturnStr(common.CCErrWebFileContentFail,
			defErr.Errorf(common.CCErrWebFileContentFail, err.Error()).Error(),
			nil)
		return err, errMsg, nil
	}
	if 0 != len(errMsgs) {
		errMsg = getReturnStr(common.CCErrWebFileContentFail,
			defErr.Errorf(common.CCErrWebFileContentFail, " file empty").Error(),
			common.KvMap{"err": errMsgs})
		return err, errMsg, nil
	}
	if 0 == len(netDevice) {
		errMsg = getReturnStr(common.CCErrWebFileContentEmpty,
			defErr.Errorf(common.CCErrWebFileContentEmpty, "").Error(), nil)
		return err, errMsg, nil
	}

	return nil, "", netDevice
}

func rebuildDeviceReponseBody(reply string, line_numbers []int) (error, string) {
	replyBody := new(meta.Response)
	if err := json.Unmarshal([]byte(reply), replyBody); nil != err {
		blog.Errorf("[Import Net Device] unmarshal response body err: %v", err)
		return err, ""
	}

	addDeviceResult, ok := replyBody.Data.([]interface{})
	if !ok {
		blog.Errorf("[Import Net Device] 'Data' field of response body convert to []interface{} fail, replyBody.Data %#+v", replyBody.Data)
		return fmt.Errorf("convert response body fail"), ""
	}

	var (
		errRow  []string
		succRow []string
	)
	for i, value := range addDeviceResult {
		data, ok := value.(map[string]interface{})
		if !ok {
			blog.Errorf("[Import Net Device] traverse replyBody.Data convert to map[string]interface{} fail, data %#+v", data)
			return fmt.Errorf("convert response body fail"), ""
		}

		result, ok := data["result"].(bool)
		if !ok {
			blog.Errorf("[Import Net Device] data is not bool: %#+v", data["result"])
			return fmt.Errorf("convert response body fail"), ""
		}

		switch result {
		case true:
			succRow = append(succRow, strconv.Itoa(line_numbers[i]))
		case false:
			errMsg, ok := data["error_msg"].(string)
			if !ok {
				blog.Errorf("[Import Net Device] data is not string: %#+v", data["error_msg"])
				return fmt.Errorf("convert response body fail"), ""
			}

			errRow = append(errRow, fmt.Sprintf("%d行%s", line_numbers[i], errMsg))
		}
	}

	retData := make(map[string]interface{})
	if 0 < len(succRow) {
		retData["success"] = succRow
	}
	if 0 < len(errRow) {
		retData["error"] = errRow
	}

	replyBody.Data = retData

	replyByte, err := json.Marshal(replyBody)
	if nil != err {
		blog.Errorf("[Import Net Device] convert rebuilded response body fail, error: %v", err)
		return err, ""
	}

	return nil, string(replyByte)
}
