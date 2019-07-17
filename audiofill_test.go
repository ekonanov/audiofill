package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"os"
	"path"
	"reflect"
	"strings"
	"testing"

	_ "github.com/lib/pq"
)

//CRC32 контрольная сумма файла-образца аудиозаписи, получена системной утилитой linux
//	>:~/go/src/backend$ crc32 media/sample.ogg
//	>efe71b98
const crcSample = 0xefe71b98

type testAudio struct {
	Method string
	Path   string
	Query  string
	Cookie *http.Cookie
	Status int
	Error  string
	Body   tAudioList
}

//String is the pretty print the AudioList structure for tests report
func (al tAudioList) String() (s string) {
	s = fmt.Sprintf("Count: %d\nList: [\n", al.Count)
	for _, v := range al.List {
		s += fmt.Sprintf("\t{AudioID: %d\n\tDescr: %s\n\tIsOwn: %#v\n\tOwnerID: %d\n\tOwnerName: %s\n\tShared: [\n", v.AudioID, v.Descr, v.IsOwn, v.OwnerID, v.OwnerName)
		for _, x := range v.Shared {
			s += fmt.Sprintf("\t\t{UserID: %d,\tUserName: %s}\n", x.UserID, x.UserName)
		}
		s += "\t\t]\n\t},\n"
	}
	s += "]\n"
	return s
}

func TestAudioList(t *testing.T) {
	var (
		client *http.Client
		req    *http.Request
		resp   *http.Response
		err    error
		result tAudioList

		testName string
	)

	client = testSrv.Client()
	cookAdmin := &http.Cookie{Name: "session_id", Value: "3d73274ac8b18ab09528075c7fee1213"}
	cookUser := &http.Cookie{Name: "session_id", Value: "b00f30ecdfa4d5bd2e5280ab59be492a"}
	cookGuest := &http.Cookie{Name: "session_id", Value: "0414d6d5d923b0f4998556df2fe2e351"}

	tests := []testAudio{
		testAudio{ //	0 недопустимый метод
			Method: http.MethodPost,
			Path:   "/audio/list",
			Cookie: cookAdmin,
			Status: http.StatusMethodNotAllowed,
			Error:  "bad method\n",
		},
		testAudio{ //	1 неавторизованный доступ
			Method: http.MethodGet,
			Path:   "/audio/list",
			Status: http.StatusUnauthorized,
			Error:  "access denied\n",
		},
		testAudio{ //	2 параметры по умолчанию
			Method: http.MethodGet,
			Path:   "/audio/list",
			Cookie: cookGuest,
			Status: http.StatusOK,
			Body: tAudioList{
				Count: 2,
				List: []*tAudio{
					&tAudio{AudioID: 1,
						Descr:     "test music (00:04:00)",
						IsOwn:     false,
						OwnerID:   1,
						OwnerName: "admin",
						Shared: []*tShare{
							&tShare{UserID: 2, UserName: "Lorem Ipsum"},
							&tShare{UserID: 3, UserName: "Uninvited T"},
						},
					},
					&tAudio{AudioID: 3,
						Descr:     "bad music (00:01:00)",
						IsOwn:     false,
						OwnerID:   2,
						OwnerName: "Lorem Ipsum",
						Shared: []*tShare{
							&tShare{UserID: 1, UserName: "admin"},
							&tShare{UserID: 3, UserName: "Uninvited T"},
						},
					},
				},
			},
		},
		testAudio{ //	3 постраничная разбивка
			Method: http.MethodGet,
			Path:   "/audio/list",
			Query:  "on_page=2",
			Cookie: cookAdmin,
			Status: http.StatusOK,
			Body: tAudioList{
				Count: 3,
				List: []*tAudio{
					&tAudio{AudioID: 2,
						Descr:     "best music (00:14:00)",
						IsOwn:     true,
						OwnerID:   1,
						OwnerName: "admin",
						Shared: []*tShare{
							&tShare{UserID: 2, UserName: "Lorem Ipsum"},
						},
					},
					&tAudio{AudioID: 1,
						Descr:     "test music (00:04:00)",
						IsOwn:     true,
						OwnerID:   1,
						OwnerName: "admin",
						Shared: []*tShare{
							&tShare{UserID: 2, UserName: "Lorem Ipsum"},
							&tShare{UserID: 3, UserName: "Uninvited T"},
						},
					},
				},
			},
		},
		testAudio{ //	4 другой пользователь, сортировка
			Method: http.MethodGet,
			Path:   "/audio/list",
			Query:  "on_page=2&order_by=track",
			Cookie: cookUser,
			Status: http.StatusOK,
			Body: tAudioList{
				Count: 4,
				List: []*tAudio{
					&tAudio{AudioID: 3,
						Descr:     "bad music (00:01:00)",
						IsOwn:     true,
						OwnerID:   2,
						OwnerName: "Lorem Ipsum",
						Shared: []*tShare{
							&tShare{UserID: 1, UserName: "admin"},
							&tShare{UserID: 3, UserName: "Uninvited T"},
						},
					},
					&tAudio{AudioID: 2,
						Descr:     "best music (00:14:00)",
						IsOwn:     false,
						OwnerID:   1,
						OwnerName: "admin",
						Shared: []*tShare{
							&tShare{UserID: 2, UserName: "Lorem Ipsum"},
						},
					},
				},
			},
		},
		testAudio{ //	5 неверный параметр сортировка
			Method: http.MethodGet,
			Path:   "/audio/list",
			Query:  "order_by=wrong",
			Cookie: cookUser,
			Status: http.StatusBadRequest,
			Error:  "bad parameter order_by\n",
		},
	}

	for idx, tst := range tests {
		testName = fmt.Sprintf("Audio.List: test [%d] %s ? %s", idx, tst.Method, tst.Query)

		if tst.Method == http.MethodGet {
			req, err = http.NewRequest(tst.Method, testSrv.URL+tst.Path+"?"+tst.Query, nil)
		} else {
			req, err = http.NewRequest(tst.Method, testSrv.URL+tst.Path, strings.NewReader(tst.Query))
			req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
		}
		if tst.Cookie != nil {
			req.AddCookie(tst.Cookie)
		}

		resp, err = client.Do(req)
		if err != nil {
			t.Fatalf("%s >> query fail %s", testName, err.Error())
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode != tst.Status {
			t.Errorf("%s >>> wrong status %d, expected %d\n", testName, resp.StatusCode, tst.Status)
			continue
		}

		respBody, _ := ioutil.ReadAll(resp.Body)
		//	успешный ответ преобразуем в структуру, ошибка — просто текст об ошибке
		//	вообще, ошибку тоже можно было бы вернуть в JSON-формате, но это все равно
		//	была бы другая структура (tError, напр) и условие пришлось бы оставить для
		//	определения типа структуры…  Оставил как есть, простой текст
		if resp.StatusCode == http.StatusOK {
			result = tAudioList{}
			err = json.Unmarshal(respBody, &result)
			if err != nil {
				t.Errorf("%s >>> unmarshaling result error [%s]", testName, err.Error())
				continue
			}
			if !reflect.DeepEqual(tst.Body, result) {
				t.Errorf("%s >>> wrong body [%s], expected [%s]\n", testName, result.String(), tst.Body.String())
			}

		} else if tst.Error != string(respBody) {
			t.Errorf("%s >>> wrong body [%s], expected [%s]\n", testName, respBody, tst.Error)
		}
	}

}

func TestAudioShare(t *testing.T) {
	var (
		client *http.Client
		err    error
		req    *http.Request
		resp   *http.Response
		result tAudioList

		testName string
	)

	client = testSrv.Client()
	cookAdmin := &http.Cookie{Name: "session_id", Value: "3d73274ac8b18ab09528075c7fee1213"}
	cookUser := &http.Cookie{Name: "session_id", Value: "b00f30ecdfa4d5bd2e5280ab59be492a"}
	cookGuest := &http.Cookie{Name: "session_id", Value: "0414d6d5d923b0f4998556df2fe2e351"}

	tests := []testAudio{
		testAudio{ //	0	недопустимый метод
			Method: http.MethodGet,
			Path:   "/audio/share",
			Status: http.StatusMethodNotAllowed,
			Error:  "bad method\n",
		},
		testAudio{ //	1	неавторизованный доступ
			Method: http.MethodPost,
			Path:   "/audio/share",
			Status: http.StatusUnauthorized,
			Error:  "access denied\n",
		},
		testAudio{ //	2	отсутствует обязательный параметр track
			Method: http.MethodPost,
			Path:   "/audio/share",
			Cookie: cookAdmin,
			Status: http.StatusBadRequest,
			Error:  "track required\n",
		},
		testAudio{ //	3	отсутствует обязательный параметр user
			Method: http.MethodPost,
			Path:   "/audio/share",
			Cookie: cookUser,
			Query:  "track=2",
			Status: http.StatusBadRequest,
			Error:  "user required\n",
		},
		testAudio{ //	4	неверный параметр track
			Method: http.MethodPost,
			Path:   "/audio/share",
			Query:  "track=%31%20or%20true&user=4",
			Cookie: cookAdmin,
			Status: http.StatusBadRequest,
			Error:  "invalid track value\n",
		},
		testAudio{ //	5	неверный параметр user
			Method: http.MethodPost,
			Path:   "/audio/share",
			Query:  "track=1&user=bad",
			Cookie: cookAdmin,
			Status: http.StatusBadRequest,
			Error:  "invalid user value\n",
		},
		testAudio{ //	6	"чужой" трек
			Method: http.MethodPost,
			Path:   "/audio/share",
			Query:  "track=1&user=4",
			Cookie: cookGuest,
			Status: http.StatusForbidden,
			Error:  "access denied\n",
		},
		testAudio{ //	7	несуществующий пользователь
			Method: http.MethodPost,
			Path:   "/audio/share",
			Query:  "track=1&user=1000",
			Cookie: cookAdmin,
			Status: http.StatusBadRequest,
			Error:  "user not exists\n",
		},
		testAudio{ //	8	успешно
			Method: http.MethodPost,
			Path:   "/audio/share",
			Query:  "track=2&user=3",
			Cookie: cookAdmin,
			Status: http.StatusOK,
		},
		testAudio{ //	9	проверим — трек с id=2 должен появиться в списке
			Method: http.MethodGet,
			Path:   "/audio/list",
			Cookie: cookGuest,
			Status: http.StatusOK,
			Body: tAudioList{
				Count: 3,
				List: []*tAudio{
					&tAudio{AudioID: 2,
						Descr:     "best music (00:14:00)",
						IsOwn:     false,
						OwnerID:   1,
						OwnerName: "admin",
						Shared: []*tShare{
							&tShare{UserID: 2, UserName: "Lorem Ipsum"},
							&tShare{UserID: 3, UserName: "Uninvited T"},
						},
					},
					&tAudio{AudioID: 1,
						Descr:     "test music (00:04:00)",
						IsOwn:     false,
						OwnerID:   1,
						OwnerName: "admin",
						Shared: []*tShare{
							&tShare{UserID: 2, UserName: "Lorem Ipsum"},
							&tShare{UserID: 3, UserName: "Uninvited T"},
						},
					},
					&tAudio{AudioID: 3,
						Descr:     "bad music (00:01:00)",
						IsOwn:     false,
						OwnerID:   2,
						OwnerName: "Lorem Ipsum",
						Shared: []*tShare{
							&tShare{UserID: 1, UserName: "admin"},
							&tShare{UserID: 3, UserName: "Uninvited T"},
						},
					},
				},
			},
		},
		testAudio{ //	10	уберем
			Method: http.MethodPost,
			Path:   "/audio/lock",
			Query:  "track=2&user=3",
			Cookie: cookAdmin,
			Status: http.StatusOK,
		},
	}

	for idx, tst := range tests {
		testName = fmt.Sprintf("Audio.Share: test [%d] %s ? %s", idx, tst.Method, tst.Query)

		if tst.Method == http.MethodGet {
			req, err = http.NewRequest(tst.Method, testSrv.URL+tst.Path+"?"+tst.Query, nil)
		} else {
			req, err = http.NewRequest(tst.Method, testSrv.URL+tst.Path, strings.NewReader(tst.Query))
			req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
		}
		if tst.Cookie != nil {
			req.AddCookie(tst.Cookie)
		}

		resp, err = client.Do(req)
		if err != nil {
			t.Fatalf("%s >> query fail %s", testName, err.Error())
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode != tst.Status {
			t.Errorf("%s >>> wrong status %d, expected %d\n", testName, resp.StatusCode, tst.Status)
			continue
		}

		respBody, _ := ioutil.ReadAll(resp.Body)
		//	успешный ответ преобразуем в структуру, ошибка — просто текст об ошибке
		if resp.StatusCode == http.StatusOK {
			if reflect.DeepEqual(tst.Body, tAudioList{}) {
				continue
			}
			result = tAudioList{}
			err = json.Unmarshal(respBody, &result)
			if err != nil {
				t.Errorf("%s >>> unmarshaling result error [%s]", testName, err.Error())
				continue
			}
			if !reflect.DeepEqual(tst.Body, result) {
				t.Errorf("%s >>> wrong body [%s], expected [%s]\n", testName, result.String(), tst.Body.String())
			}

		} else if tst.Error != string(respBody) {
			t.Errorf("%s >>> wrong body [%s], expected [%s]\n", testName, respBody, tst.Error)
		}
	}
}

func TestAudioLock(t *testing.T) {
	var (
		client *http.Client
		req    *http.Request
		resp   *http.Response
		err    error

		testName string
		result   tAudioList
	)

	client = testSrv.Client()
	cookAdmin := &http.Cookie{Name: "session_id", Value: "3d73274ac8b18ab09528075c7fee1213"}
	cookUser := &http.Cookie{Name: "session_id", Value: "b00f30ecdfa4d5bd2e5280ab59be492a"}
	cookGuest := &http.Cookie{Name: "session_id", Value: "0414d6d5d923b0f4998556df2fe2e351"}

	tests := []testAudio{
		testAudio{ //	0	недопустимый метод
			Method: http.MethodGet,
			Path:   "/audio/lock",
			Status: http.StatusMethodNotAllowed,
			Error:  "bad method\n",
		},
		testAudio{ //	1	неавторизованный доступ
			Method: http.MethodPost,
			Path:   "/audio/lock",
			Status: http.StatusUnauthorized,
			Error:  "access denied\n",
		},
		testAudio{ //	2	отсутствует обязательный параметр track
			Method: http.MethodPost,
			Path:   "/audio/lock",
			Cookie: cookAdmin,
			Status: http.StatusBadRequest,
			Error:  "track required\n",
		},
		testAudio{ //	3	отсутствует обязательный параметр user
			Method: http.MethodPost,
			Path:   "/audio/lock",
			Cookie: cookUser,
			Query:  "track=2",
			Status: http.StatusBadRequest,
			Error:  "user required\n",
		},
		testAudio{ //	4	неверный параметр track
			Method: http.MethodPost,
			Path:   "/audio/lock",
			Query:  "track=%31%20or%20true&user=4",
			Cookie: cookAdmin,
			Status: http.StatusBadRequest,
			Error:  "invalid track value\n",
		},
		testAudio{ //	5	неверный параметр user
			Method: http.MethodPost,
			Path:   "/audio/lock",
			Query:  "track=1&user=bad",
			Cookie: cookAdmin,
			Status: http.StatusBadRequest,
			Error:  "invalid user value\n",
		},
		testAudio{ //	6	"чужой" трек
			Method: http.MethodPost,
			Path:   "/audio/lock",
			Query:  "track=1&user=4",
			Cookie: cookGuest,
			Status: http.StatusForbidden,
			Error:  "access denied\n",
		},
		testAudio{ //	7	несуществующий пользователь
			Method: http.MethodPost,
			Path:   "/audio/lock",
			Query:  "track=1&user=1000",
			Cookie: cookAdmin,
			Status: http.StatusNotFound,
			Error:  "no rows are deleted\n",
		},
		testAudio{ //	8	успешно
			Method: http.MethodPost,
			Path:   "/audio/lock",
			Query:  "track=1&user=3",
			Cookie: cookAdmin,
			Status: http.StatusOK,
			Body:   tAudioList{},
		},
		testAudio{ //	9	должная остаться одна
			Method: http.MethodGet,
			Path:   "/audio/list",
			Cookie: cookGuest,
			Status: http.StatusOK,
			Body: tAudioList{
				Count: 1,
				List: []*tAudio{
					&tAudio{AudioID: 3,
						Descr:     "bad music (00:01:00)",
						IsOwn:     false,
						OwnerID:   2,
						OwnerName: "Lorem Ipsum",
						Shared: []*tShare{
							&tShare{UserID: 1, UserName: "admin"},
							&tShare{UserID: 3, UserName: "Uninvited T"},
						},
					},
				},
			},
		},
		testAudio{ //	10	еще одно уберем
			Method: http.MethodPost,
			Path:   "/audio/lock",
			Query:  "track=3&user=3",
			Cookie: cookUser,
			Status: http.StatusOK,
			Body:   tAudioList{},
		},
		testAudio{ //	11 нет записей
			Method: http.MethodGet,
			Path:   "/audio/list",
			Cookie: cookGuest,
			Status: http.StatusNotFound,
			Error:  "\n",
		},
	}

	for idx, tst := range tests {
		testName = fmt.Sprintf("Audio.Lock: test [%d] %s ? %s", idx, tst.Method, tst.Query)

		if tst.Method == http.MethodGet {
			req, err = http.NewRequest(tst.Method, testSrv.URL+tst.Path+"?"+tst.Query, nil)
		} else {
			req, err = http.NewRequest(tst.Method, testSrv.URL+tst.Path, strings.NewReader(tst.Query))
			req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
		}
		if tst.Cookie != nil {
			req.AddCookie(tst.Cookie)
		}

		resp, err = client.Do(req)
		if err != nil {
			t.Fatalf("%s >> query fail %s", testName, err.Error())
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode != tst.Status {
			t.Errorf("%s >>> wrong status %d, expected %d\n", testName, resp.StatusCode, tst.Status)
			continue
		}

		respBody, _ := ioutil.ReadAll(resp.Body)
		//	успешный ответ преобразуем в структуру, ошибка — просто текст об ошибке
		if resp.StatusCode == http.StatusOK {
			if reflect.DeepEqual(tst.Body, tAudioList{}) {
				continue
			}
			result = tAudioList{}
			err = json.Unmarshal(respBody, &result)
			if err != nil {
				t.Errorf("%s >>> unmarshaling result error [%s]", testName, err.Error())
				continue
			}
			if !reflect.DeepEqual(tst.Body, result) {
				t.Errorf("%s >>> wrong body [%s], expected [%s]\n", testName, result.String(), tst.Body.String())
			}

		} else if tst.Error != string(respBody) {
			t.Errorf("%s >>> wrong body [%s], expected [%s]\n", testName, respBody, tst.Error)
		}
	}
}

func TestAudioGet(t *testing.T) {
	var (
		err  error
		req  *http.Request
		resp *http.Response

		testName string
	)

	client := testSrv.Client()
	cookAdmin := &http.Cookie{Name: "session_id", Value: "3d73274ac8b18ab09528075c7fee1213"}
	tests := []testAudio{
		testAudio{ //	0 bad method
			Method: http.MethodPut,
			Path:   "/audio/get",
			Status: http.StatusMethodNotAllowed,
			Error:  "bad method\n",
		},
		testAudio{ //	1 unauthorized
			Method: http.MethodGet,
			Path:   "/audio/get",
			Status: http.StatusUnauthorized,
			Error:  "access denied\n",
		},
		testAudio{ //	2 parameter track required
			Method: http.MethodGet,
			Path:   "/audio/get",
			Cookie: cookAdmin,
			Status: http.StatusBadRequest,
			Error:  "track required\n",
		},
		testAudio{ //	3 parameter track invalid
			Method: http.MethodGet,
			Path:   "/audio/get",
			Cookie: cookAdmin,
			Query:  "track=1'and%20true",
			Status: http.StatusBadRequest,
			Error:  "track invalid value\n",
		},
		testAudio{ //	4 file not exists
			Method: http.MethodGet,
			Path:   "/audio/get",
			Cookie: cookAdmin,
			Query:  "track=2",
			Status: http.StatusNotFound,
			Error:  "file not found\n",
		},
	}

	for idx, tst := range tests {
		testName = fmt.Sprintf("Audio.Get: test [%d] %s ? %s", idx, tst.Method, tst.Query)

		if tst.Method == http.MethodGet {
			req, err = http.NewRequest(tst.Method, testSrv.URL+tst.Path+"?"+tst.Query, nil)
		} else {
			req, err = http.NewRequest(tst.Method, testSrv.URL+tst.Path, strings.NewReader(tst.Query))
			req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
		}
		if tst.Cookie != nil {
			req.AddCookie(tst.Cookie)
		}

		resp, err = client.Do(req)
		if err != nil {
			t.Fatalf("%s >> query fail %s", testName, err.Error())
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode != tst.Status {
			t.Errorf("%s >>> wrong status %d, expected %d\n", testName, resp.StatusCode, tst.Status)
			continue
		}

		respBody, _ := ioutil.ReadAll(resp.Body)
		if resp.StatusCode == http.StatusOK {

		} else if tst.Error != string(respBody) {
			t.Errorf("%s >>> wrong body [%s], expected [%s]\n", testName, respBody, tst.Error)
		}
	}

	//	успешный запрос ­— получаем файл, проверяем CRC
	req, _ = http.NewRequest(http.MethodGet, testSrv.URL+"/audio/get?track=1", nil)
	req.AddCookie(cookAdmin)

	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("Audio.Get test GET?track=1 >>> query failed %s", err.Error())

	} else {
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Audio.Get test GET?track=1 >>> wrong status %d, expected %d", resp.StatusCode, http.StatusOK)

		} else {
			respBody, _ := ioutil.ReadAll(resp.Body)
			hashBody := crc32.ChecksumIEEE(respBody)
			if hashBody != crcSample {
				t.Errorf("Audio.Get test GET?track=1 >>> wrong CRC summ %d, expected %d", hashBody, crcSample)
			}
		}
	}
}

func TestAudioAdd(t *testing.T) {
	var (
		err  error
		req  *http.Request
		resp *http.Response

		testName string
	)

	client := testSrv.Client()
	cookAdmin := &http.Cookie{Name: "session_id", Value: "3d73274ac8b18ab09528075c7fee1213"}
	cookUser := &http.Cookie{Name: "session_id", Value: "b00f30ecdfa4d5bd2e5280ab59be492a"}

	tests := []testAudio{
		testAudio{ //	0 bad method
			Method: http.MethodGet,
			Path:   "/audio/add",
			Status: http.StatusMethodNotAllowed,
			Error:  "bad method\n",
		},
		testAudio{ //	1 unauthorized
			Method: http.MethodPut,
			Path:   "/audio/add",
			Status: http.StatusUnauthorized,
			Error:  "access denied\n",
		},
		testAudio{ //	1 unauthorized
			Method: http.MethodPut,
			Path:   "/audio/add",
			Status: http.StatusUnauthorized,
			Error:  "access denied\n",
		},
	}

	for idx, tst := range tests {
		testName = fmt.Sprintf("Audio.Add: test [%d] %s ? %s", idx, tst.Method, tst.Query)

		if tst.Method == http.MethodGet {
			req, err = http.NewRequest(tst.Method, testSrv.URL+tst.Path+"?"+tst.Query, nil)
		} else {
			req, err = http.NewRequest(tst.Method, testSrv.URL+tst.Path, strings.NewReader(tst.Query))
			req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
		}
		if tst.Cookie != nil {
			req.AddCookie(tst.Cookie)
		}

		resp, err = client.Do(req)
		if err != nil {
			t.Fatalf("%s >> query fail %s", testName, err.Error())
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode != tst.Status {
			t.Errorf("%s >>> wrong status %d, expected %d\n", testName, resp.StatusCode, tst.Status)
			continue
		}

		respBody, _ := ioutil.ReadAll(resp.Body)
		if resp.StatusCode == http.StatusOK {

		} else if tst.Error != string(respBody) {
			t.Errorf("%s >>> wrong body [%s], expected [%s]\n", testName, respBody, tst.Error)
		}
	}

	buf := &bytes.Buffer{}
	if fd, err := os.Open("wings.mp3"); err == nil {
		defer fd.Close()

		//	загрузка файла — параметры указаны
		frmData := multipart.NewWriter(buf)
		frmData.WriteField("name", "upload test")
		frmData.WriteField("duration", "00:03:45")
		frmFile, _ := frmData.CreateFormFile("file", path.Base(fd.Name()))
		io.Copy(frmFile, fd)
		frmData.Close()

		req, _ = http.NewRequest(http.MethodPut, testSrv.URL+"/audio/add", buf)
		req.AddCookie(cookAdmin)
		req.Header.Add("Content-Type", frmData.FormDataContentType())

		resp, err = client.Do(req)
		if err != nil {
			t.Fatalf("Audio.Add test PUT wings.mp3 >>> quiery failed %s", err.Error())
		} else {
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Errorf("Audio.Add test PUT wings.mp3 >>> wrong status %d, expected %d", resp.StatusCode, http.StatusOK)
			}
		}

		//	загрузка файла — параметры по умолчанию
		buf.Reset()
		fd.Seek(0, 0)
		frmData = multipart.NewWriter(buf)
		frmFile, _ = frmData.CreateFormFile("file", path.Base(fd.Name()))
		io.Copy(frmFile, fd)
		frmData.Close()

		req, _ = http.NewRequest(http.MethodPut, testSrv.URL+"/audio/add", buf)
		req.AddCookie(cookUser)
		req.Header.Add("Content-Type", frmData.FormDataContentType())

		resp, err = client.Do(req)
		if err != nil {
			t.Fatalf("Audio.Add test PUT wings.mp3 >>> quiery failed %s", err.Error())
		} else {
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Errorf("Audio.Add test PUT wings.mp3 >>> wrong status %d, expected %d", resp.StatusCode, http.StatusOK)
			}
		}
	} else {
		t.Error("cant open file wings.mp3. Test failed")
	}
}
