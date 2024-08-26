package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/kyma-project/btp-manager/internal/api"
	"github.com/kyma-project/btp-manager/internal/api/requests"
	"github.com/kyma-project/btp-manager/internal/api/responses"
	clusterobject "github.com/kyma-project/btp-manager/internal/cluster-object"
	servicemanager "github.com/kyma-project/btp-manager/internal/service-manager"
	"github.com/kyma-project/btp-manager/internal/service-manager/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type MockSMClient struct {
	fakeSMClient api.SMClient
}

const (
	port         = "8080"
	readTimeout  = 1 * time.Second
	writeTimeout = 1 * time.Second
	idleTimeout  = 2 * time.Second
)

func TestAPI(t *testing.T) {
	// before all
	cfg := api.Config{
		Port:         port,
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
		IdleTimeout:  idleTimeout,
	}
	defaultSIs := defaultServiceInstances()
	defaultSBs := defaultServiceBindings()

	fakeSM, err := servicemanager.NewFakeServer()
	require.NoError(t, err)

	fakeSM.Start()
	defer fakeSM.Close()
	httpClient := fakeSM.Client()
	url := fakeSM.URL

	secretMgr := clusterobject.NewFakeSecretManager()
	err = secretMgr.Create(context.TODO(), clusterobject.FakeDefaultSecret())
	require.NoError(t, err)

	fakeSMClient := servicemanager.NewClient(context.TODO(), slog.Default(), secretMgr)
	fakeSMClient.SetHTTPClient(httpClient)
	fakeSMClient.SetSMURL(url)
	mockSMClient := &MockSMClient{
		fakeSMClient: fakeSMClient,
	}
	btpMgrAPI := api.NewAPI(cfg, mockSMClient, secretMgr, nil)
	apiAddr := "http://localhost" + btpMgrAPI.Address()
	go btpMgrAPI.Start()

	apiClient := http.Client{
		Timeout: 500 * time.Millisecond,
	}

	t.Run("GET Service Instances", func(t *testing.T) {
		// when
		req, err := http.NewRequest(http.MethodGet, apiAddr+"/api/service-instances?sm_secret_name=sap-btp-service-operator&sm_secret_namespace=kyma-system", nil)
		resp, err := apiClient.Do(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		defer resp.Body.Close()

		var sis responses.ServiceInstances
		err = json.NewDecoder(resp.Body).Decode(&sis)
		require.NoError(t, err)

		// then
		assert.Equal(t, sis.NumItems, 4)
		assert.ElementsMatch(t, sis.Items, defaultSIs.Items)
	})

	t.Run("GET Service Instances 403 error", func(t *testing.T) {
		// when
		fakeSM.RespondWithErrors()
		req, err := http.NewRequest(http.MethodGet, apiAddr+"/api/service-instances", nil)
		resp, err := apiClient.Do(req)
		require.NoError(t, err)

		// then
		require.Equal(t, http.StatusForbidden, resp.StatusCode)
		fakeSM.RespondWithData()
	})

	t.Run("GET Service Instance by ID", func(t *testing.T) {
		// given
		siID := "a7e240d6-e348-4fc0-a54c-7b7bfe9b9da6"
		expectedSI := getServiceInstanceByID(defaultSIs, siID)
		expectedSI.ServicePlanID = "4036790e-5ef3-4cf7-bb16-476053477a9a"
		expectedSI.ServicePlanName = "service1-plan2"

		// when
		req, err := http.NewRequest(http.MethodGet, apiAddr+"/api/service-instances?id="+siID, nil)
		resp, err := apiClient.Do(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		defer resp.Body.Close()

		var si responses.ServiceInstance
		err = json.NewDecoder(resp.Body).Decode(&si)
		require.NoError(t, err)

		// then
		assert.Equal(t, expectedSI, si)
	})

	t.Run("GET Service Instance by ID 400 error", func(t *testing.T) {
		// when
		siID := "a7e240d6-e348-4fc0-a54c-7b7bfe9b9da6"
		fakeSM.RespondWithErrors()
		req, err := http.NewRequest(http.MethodGet, apiAddr+"/api/service-instances?id="+siID, nil)
		resp, err := apiClient.Do(req)
		require.NoError(t, err)

		// then
		require.Equal(t, http.StatusBadRequest, resp.StatusCode)
		fakeSM.RespondWithData()
	})

	t.Run("POST Service Instance", func(t *testing.T) {
		// given
		siToCreate := requests.CreateServiceInstance{
			Name:          "new-si-01",
			ServicePlanID: "4036790e-5ef3-4cf7-bb16-476053477a9a",
			Namespace:     "kyma-system",
			ClusterID:     "test-cluster-id",
			Labels: map[string][]string{
				"label1": {"value1"},
				"label2": {"value2a", "value2b"},
			},
			Parameters: []byte(`{"param1": "value1", "param2": "value2"}`),
		}
		siToCreateJSON, err := json.Marshal(siToCreate)
		require.NoError(t, err)

		expectedSI := responses.ServiceInstance{
			Name:          "new-si-01",
			Namespace:     "kyma-system",
			ServicePlanID: "4036790e-5ef3-4cf7-bb16-476053477a9a",
			ClusterID:     "test-cluster-id",
		}

		// when
		req, err := http.NewRequest(http.MethodPost, apiAddr+"/api/service-instances", bytes.NewBuffer(siToCreateJSON))
		resp, err := apiClient.Do(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, resp.StatusCode)
		defer resp.Body.Close()

		var si responses.ServiceInstance
		err = json.NewDecoder(resp.Body).Decode(&si)
		require.NoError(t, err)

		// then
		assert.NotEmpty(t, si.ID)
		assert.NotEmpty(t, si.SubaccountID)
		expectedSI.ID = si.ID                     // ID is generated by the server
		expectedSI.SubaccountID = si.SubaccountID // SubaccountID is determined from the secret
		assert.Equal(t, expectedSI, si)

		err = fakeSM.RestoreDefaults()
		require.NoError(t, err)
	})

	t.Run("POST Service Instance 422 error", func(t *testing.T) {
		// given
		siToCreate := requests.CreateServiceInstance{
			Name:          "new-si-01",
			ServicePlanID: "4036790e-5ef3-4cf7-bb16-476053477a9a",
			Namespace:     "kyma-system",
			ClusterID:     "test-cluster-id",
			Labels: map[string][]string{
				"label1": {"value1"},
				"label2": {"value2a", "value2b"},
			},
			Parameters: []byte(`{"param1": "value1", "param2": "value2"}`),
		}
		siToCreateJSON, err := json.Marshal(siToCreate)
		require.NoError(t, err)

		fakeSM.RespondWithErrors()
		req, err := http.NewRequest(http.MethodPost, apiAddr+"/api/service-instances", bytes.NewBuffer(siToCreateJSON))
		resp, err := apiClient.Do(req)
		require.NoError(t, err)

		// then
		require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
		fakeSM.RespondWithData()
	})

	t.Run("PATCH Service Instance by ID", func(t *testing.T) {
		// given
		siID := "a7e240d6-e348-4fc0-a54c-7b7bfe9b9da6"
		expectedSI := getServiceInstanceByID(defaultSIs, siID)
		expectedSI.Name = "a7e240d6-updated-si"
		expectedSI.ServicePlanID = "61772d7e-4e67-48f5-90fc-dd9254aa454b"
		expectedSI.ServicePlanName = "service3-plan1"

		siToUpdate := types.ServiceInstanceUpdateRequest{
			Name:          &expectedSI.Name,
			ServicePlanID: &expectedSI.ServicePlanID,
		}

		siToUpdateJSON, err := json.Marshal(siToUpdate)
		require.NoError(t, err)

		// when
		req, err := http.NewRequest(http.MethodPatch, apiAddr+"/api/service-instances?id="+siID, bytes.NewBuffer(siToUpdateJSON))
		resp, err := apiClient.Do(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		defer resp.Body.Close()

		var si responses.ServiceInstance
		err = json.NewDecoder(resp.Body).Decode(&si)
		require.NoError(t, err)

		// then
		assert.Equal(t, expectedSI.Name, si.Name)
		assert.Equal(t, expectedSI.ServicePlanID, si.ServicePlanID)

		err = fakeSM.RestoreDefaults()
		require.NoError(t, err)
	})

	t.Run("PATCH Service Instance by ID 422 error", func(t *testing.T) {
		// given
		siID := "a7e240d6-e348-4fc0-a54c-7b7bfe9b9da6"
		name := "a7e240d6-updated-si"
		servicePlanID := "61772d7e-4e67-48f5-90fc-dd9254aa454b"

		siToUpdate := types.ServiceInstanceUpdateRequest{
			Name:          &name,
			ServicePlanID: &servicePlanID,
		}

		siToUpdateJSON, err := json.Marshal(siToUpdate)
		require.NoError(t, err)

		fakeSM.RespondWithErrors()
		req, err := http.NewRequest(http.MethodPatch, apiAddr+"/api/service-instances?id="+siID, bytes.NewBuffer(siToUpdateJSON))
		resp, err := apiClient.Do(req)
		require.NoError(t, err)

		// then
		require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
		fakeSM.RespondWithData()
	})

	t.Run("DELETE Service Instance by ID", func(t *testing.T) {
		// given
		siID := "a7e240d6-e348-4fc0-a54c-7b7bfe9b9da6"

		// when
		req, err := http.NewRequest(http.MethodDelete, apiAddr+"/api/service-instances?id="+siID, nil)
		resp, err := apiClient.Do(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)

		// then
		req, err = http.NewRequest(http.MethodGet, apiAddr+"/api/service-instances?id="+siID, nil)
		resp, err = apiClient.Do(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusNotFound, resp.StatusCode)

		err = fakeSM.RestoreDefaults()
		require.NoError(t, err)
	})

	t.Run("DELETE Service Instance by ID 403 error", func(t *testing.T) {
		// given
		siID := "a7e240d6-e348-4fc0-a54c-7b7bfe9b9da6"

		// when
		fakeSM.RespondWithErrors()
		req, err := http.NewRequest(http.MethodDelete, apiAddr+"/api/service-instances?id="+siID, nil)
		resp, err := apiClient.Do(req)
		require.NoError(t, err)

		// then
		require.Equal(t, http.StatusForbidden, resp.StatusCode)
		fakeSM.RespondWithData()
	})

	t.Run("GET Service Bindings", func(t *testing.T) {
		// when
		req, err := http.NewRequest(http.MethodGet, apiAddr+"/api/service-bindings", nil)
		resp, err := apiClient.Do(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		defer resp.Body.Close()

		var sbs responses.ServiceBindings
		err = json.NewDecoder(resp.Body).Decode(&sbs)
		require.NoError(t, err)

		// then
		assert.Equal(t, sbs.NumItems, 4)
		assert.ElementsMatch(t, sbs.Items, defaultSBs.Items)
	})

	t.Run("GET Service Bindings with Secrets", func(t *testing.T) {
		// given
		sb1ID, sb2ID := "550e8400-e29b-41d4-a716-446655440003", "9e420bca-4cf2-4858-ade2-e5ef23cd756f"
		ns1, ns2 := "default", "kyma-system"
		expectedSBs := defaultServiceBindingsWithSecrets()
		secret1 := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      sb1ID + "-secret",
				Namespace: ns1,
				Labels: map[string]string{
					clusterobject.ManagedByLabelKey:     clusterobject.OperatorName,
					clusterobject.ServiceBindingIDLabel: sb1ID,
				},
			},
			StringData: map[string]string{"username": "user1", "password": "pass1"},
		}
		secret2 := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      sb2ID + "-secret",
				Namespace: ns2,
				Labels: map[string]string{
					clusterobject.ManagedByLabelKey:     clusterobject.OperatorName,
					clusterobject.ServiceBindingIDLabel: sb2ID,
				},
			},
			StringData: map[string]string{"username": "user2", "password": "pass2"},
		}

		err := secretMgr.Create(context.TODO(), secret1)
		require.NoError(t, err)
		err = secretMgr.Create(context.TODO(), secret2)
		require.NoError(t, err)

		// when
		req, err := http.NewRequest(http.MethodGet, apiAddr+"/api/service-bindings", nil)
		resp, err := apiClient.Do(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		defer resp.Body.Close()

		var sbs responses.ServiceBindings
		err = json.NewDecoder(resp.Body).Decode(&sbs)
		require.NoError(t, err)

		// then
		assert.Equal(t, sbs.NumItems, 4)
		assert.ElementsMatch(t, sbs.Items, expectedSBs.Items)

		//cleanup
		err = secretMgr.Delete(context.TODO(), secret1)
		require.NoError(t, err)
		err = secretMgr.Delete(context.TODO(), secret2)
		require.NoError(t, err)
	})

	t.Run("GET Service Bindings 403 error", func(t *testing.T) {
		// when
		fakeSM.RespondWithErrors()
		req, err := http.NewRequest(http.MethodGet, apiAddr+"/api/service-bindings", nil)
		resp, err := apiClient.Do(req)
		require.NoError(t, err)

		// then
		require.Equal(t, http.StatusForbidden, resp.StatusCode)
		fakeSM.RespondWithData()
	})

	t.Run("GET Service Binding by ID", func(t *testing.T) {
		// given
		sbID := "318a16c3-7c80-485f-b55c-918629012c9a"
		expectedSB := getServiceBindingByID(defaultSBs, sbID)

		// when
		req, err := http.NewRequest(http.MethodGet, apiAddr+"/api/service-bindings?id="+sbID, nil)
		resp, err := apiClient.Do(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		defer resp.Body.Close()

		var sb responses.ServiceBinding
		err = json.NewDecoder(resp.Body).Decode(&sb)
		require.NoError(t, err)

		// then
		assert.Equal(t, expectedSB, sb)
	})

	t.Run("GET Service Binding by ID with Secret", func(t *testing.T) {
		// given
		sbID, ns := "550e8400-e29b-41d4-a716-446655440003", "default"
		sbs := defaultServiceBindingsWithSecrets()
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      sbID + "-secret",
				Namespace: ns,
				Labels: map[string]string{
					clusterobject.ManagedByLabelKey:     clusterobject.OperatorName,
					clusterobject.ServiceBindingIDLabel: sbID,
				},
			},
			StringData: map[string]string{"username": "user1", "password": "pass1"},
		}
		err := secretMgr.Create(context.TODO(), secret)
		require.NoError(t, err)

		expectedSB := getServiceBindingByID(sbs, sbID)

		// when
		req, err := http.NewRequest(http.MethodGet, apiAddr+"/api/service-bindings?id="+sbID, nil)
		resp, err := apiClient.Do(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		defer resp.Body.Close()

		var sb responses.ServiceBinding
		err = json.NewDecoder(resp.Body).Decode(&sb)
		require.NoError(t, err)

		// then
		assert.Equal(t, expectedSB, sb)

		// cleanup
		err = secretMgr.Delete(context.TODO(), secret)
		require.NoError(t, err)
	})

	t.Run("GET Service Binding by ID 400 error", func(t *testing.T) {
		// given
		sbID := "318a16c3-7c80-485f-b55c-918629012c9a"

		// when
		fakeSM.RespondWithErrors()
		req, err := http.NewRequest(http.MethodGet, apiAddr+"/api/service-bindings?id="+sbID, nil)
		resp, err := apiClient.Do(req)
		require.NoError(t, err)

		// then
		require.Equal(t, http.StatusBadRequest, resp.StatusCode)
		fakeSM.RespondWithData()
	})

	t.Run("POST Service Binding", func(t *testing.T) {
		// given
		sbCreateRequest := requests.CreateServiceBinding{
			Name:              "sb-test-01",
			ServiceInstanceID: "a7e240d6-e348-4fc0-a54c-7b7bfe9b9da6",
			Parameters:        []byte(`{"param1": "value1", "param2": "value2"}`),
			SecretName:        "binding-secret-01",
			SecretNamespace:   "default",
		}
		sbCreateRequestJSON, err := json.Marshal(sbCreateRequest)
		require.NoError(t, err)

		// when
		req, err := http.NewRequest(http.MethodPost, apiAddr+"/api/service-bindings", bytes.NewBuffer(sbCreateRequestJSON))
		resp, err := apiClient.Do(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, resp.StatusCode)
		defer resp.Body.Close()

		var sb responses.ServiceBinding
		err = json.NewDecoder(resp.Body).Decode(&sb)
		require.NoError(t, err)

		// then
		assert.NotEmpty(t, sb.ID)
		assert.Equal(t, sbCreateRequest.Name, sb.Name)

		// when
		secrets, err := secretMgr.GetAllByLabels(context.TODO(), map[string]string{
			clusterobject.ServiceBindingIDLabel:  sb.ID,
			clusterobject.ServiceInstanceIDLabel: sbCreateRequest.ServiceInstanceID,
		})
		require.NoError(t, err)

		// then
		assert.Len(t, secrets.Items, 1)

		secretMgr.Clean()
		err = fakeSM.RestoreDefaults()
		require.NoError(t, err)
	})

	t.Run("POST Service Binding with JSON object in credentials", func(t *testing.T) {
		// given
		siID := "a7e240d6-e348-4fc0-a54c-7b7bfe9b9da6"
		sbCreateRequest := requests.CreateServiceBinding{
			Name:              "sb-test-02",
			ServiceInstanceID: servicemanager.FakeJSONObjectCredentialsServiceInstanceID,
			Parameters:        []byte(`{"param1": "value1", "param2": "value2"}`),
			SecretName:        "binding-secret-02",
			SecretNamespace:   "default",
		}
		sbCreateRequestJSON, err := json.Marshal(sbCreateRequest)
		require.NoError(t, err)

		// when
		req, err := http.NewRequest(http.MethodPost, apiAddr+"/api/service-bindings", bytes.NewBuffer(sbCreateRequestJSON))
		resp, err := apiClient.Do(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, resp.StatusCode)
		defer resp.Body.Close()

		var sb responses.ServiceBinding
		err = json.NewDecoder(resp.Body).Decode(&sb)
		require.NoError(t, err)

		// then
		assert.NotEmpty(t, sb.ID)
		assert.Equal(t, sbCreateRequest.Name, sb.Name)

		// when
		secrets, err := secretMgr.GetAllByLabels(context.TODO(), map[string]string{
			clusterobject.ServiceBindingIDLabel:  sb.ID,
			clusterobject.ServiceInstanceIDLabel: siID,
		})
		require.NoError(t, err)

		// then
		assert.Len(t, secrets.Items, 1)

		secretMgr.Clean()
		err = fakeSM.RestoreDefaults()
		require.NoError(t, err)
	})

	t.Run("POST Service Binding 400 error", func(t *testing.T) {
		// given
		sbCreateRequest := requests.CreateServiceBinding{
			Name:              "sb-test-01",
			ServiceInstanceID: "a7e240d6-e348-4fc0-a54c-7b7bfe9b9da6",
			Parameters:        []byte(`{"param1": "value1", "param2": "value2"}`),
		}
		sbCreateRequestJSON, err := json.Marshal(sbCreateRequest)
		require.NoError(t, err)

		fakeSM.RespondWithErrors()
		req, err := http.NewRequest(http.MethodPost, apiAddr+"/api/service-bindings", bytes.NewBuffer(sbCreateRequestJSON))
		resp, err := apiClient.Do(req)
		require.NoError(t, err)

		// then
		require.Equal(t, http.StatusBadRequest, resp.StatusCode)
		fakeSM.RespondWithData()
	})

	t.Run("POST Service Binding 409 error", func(t *testing.T) {
		// given
		secretName, secretNamespace := "sb-test-01-secret", "default"
		sbCreateRequest := requests.CreateServiceBinding{
			Name:              "sb-test-01",
			ServiceInstanceID: "a7e240d6-e348-4fc0-a54c-7b7bfe9b9da6",
			Parameters:        []byte(`{"param1": "value1", "param2": "value2"}`),
			SecretName:        secretName,
			SecretNamespace:   secretNamespace,
		}
		sbCreateRequestJSON, err := json.Marshal(sbCreateRequest)
		require.NoError(t, err)

		existingSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretName,
				Namespace: secretNamespace,
			},
			StringData: map[string]string{"foo": "bar"},
		}
		require.NoError(t, secretMgr.Create(context.TODO(), existingSecret))

		req, err := http.NewRequest(http.MethodPost, apiAddr+"/api/service-bindings", bytes.NewBuffer(sbCreateRequestJSON))
		require.NoError(t, err)
		resp, err := apiClient.Do(req)
		require.NoError(t, err)

		defer resp.Body.Close()
		msgBytes, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		// then
		require.Equal(t, http.StatusConflict, resp.StatusCode)
		assert.Contains(t, fmt.Sprintf("secret \"%s\" in \"%s\" namespace already exists", secretName, secretNamespace), string(msgBytes))

		// cleanup
		err = secretMgr.Delete(context.TODO(), existingSecret)
	})

	t.Run("DELETE Service Binding by ID", func(t *testing.T) {
		// given
		sbID := "318a16c3-7c80-485f-b55c-918629012c9a"
		labels := map[string]string{
			clusterobject.ManagedByLabelKey:     clusterobject.OperatorName,
			clusterobject.ServiceBindingIDLabel: sbID,
		}
		secretToDelete := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "service-binding-3-secret",
				Namespace: "kyma-system",
				Labels:    labels,
			},
			StringData: map[string]string{"username": "user3", "password": "pass3"},
		}
		err := secretMgr.Create(context.TODO(), secretToDelete)
		require.NoError(t, err)

		// when
		req, err := http.NewRequest(http.MethodDelete, apiAddr+"/api/service-bindings?id="+sbID, nil)
		resp, err := apiClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		// then
		require.Equal(t, http.StatusOK, resp.StatusCode)

		// when
		req, err = http.NewRequest(http.MethodGet, apiAddr+"/api/service-bindings?id="+sbID, nil)
		resp, err = apiClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		// then
		require.Equal(t, http.StatusNotFound, resp.StatusCode)

		// when
		secrets, err := secretMgr.GetAllByLabels(context.TODO(), labels)
		require.NoError(t, err)

		// then
		assert.Len(t, secrets.Items, 0)

		err = fakeSM.RestoreDefaults()
		require.NoError(t, err)
	})

	t.Run("DELETE Service Binding by ID when no secrets are present", func(t *testing.T) {
		// given
		sbID := "318a16c3-7c80-485f-b55c-918629012c9a"

		// when
		req, err := http.NewRequest(http.MethodDelete, apiAddr+"/api/service-bindings?id="+sbID, nil)
		resp, err := apiClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		// then
		require.Equal(t, http.StatusOK, resp.StatusCode)

		// when
		req, err = http.NewRequest(http.MethodGet, apiAddr+"/api/service-bindings?id="+sbID, nil)
		resp, err = apiClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		// then
		require.Equal(t, http.StatusNotFound, resp.StatusCode)

		err = fakeSM.RestoreDefaults()
		require.NoError(t, err)
	})

	t.Run("DELETE Service Binding by ID 403 error", func(t *testing.T) {
		// given
		sbID := "318a16c3-7c80-485f-b55c-918629012c9a"

		fakeSM.RespondWithErrors()
		req, err := http.NewRequest(http.MethodDelete, apiAddr+"/api/service-bindings?id="+sbID, nil)
		resp, err := apiClient.Do(req)
		require.NoError(t, err)

		// then
		require.Equal(t, http.StatusForbidden, resp.StatusCode)
		fakeSM.RespondWithData()
	})

	t.Run("PUT Service Binding by ID (restore secret)", func(t *testing.T) {
		// given
		sbID, sbName := "318a16c3-7c80-485f-b55c-918629012c9a", "service-binding-3"
		secretName, secretNamespace := "service-binding-3-secret", "test-namespace"
		sbCreateRequest := &requests.CreateServiceBinding{
			Name:              sbName,
			ServiceInstanceID: "a7e240d6-e348-4fc0-a54c-7b7bfe9b9da6",
			SecretName:        secretName,
			SecretNamespace:   secretNamespace,
		}
		sbCreateRequestJSON, err := json.Marshal(sbCreateRequest)
		require.NoError(t, err)

		// when
		req, err := http.NewRequest(http.MethodPut, apiAddr+"/api/service-bindings/"+sbID, bytes.NewBuffer(sbCreateRequestJSON))
		require.NoError(t, err)
		resp, err := apiClient.Do(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, resp.StatusCode)

		defer resp.Body.Close()
		var sb responses.ServiceBinding
		err = json.NewDecoder(resp.Body).Decode(&sb)
		require.NoError(t, err)

		createdSecret, err := secretMgr.GetByNameAndNamespace(context.TODO(), secretName, secretNamespace)
		require.NoError(t, err)

		// then
		assert.Equal(t, createdSecret.Name, secretName)
		assert.Equal(t, createdSecret.Namespace, secretNamespace)
		assert.Equal(t, createdSecret.Labels[clusterobject.ServiceBindingIDLabel], sbID)
		assert.Equal(t, createdSecret.Labels[clusterobject.ServiceInstanceIDLabel], sbCreateRequest.ServiceInstanceID)
		assert.Equal(t, createdSecret.Labels[clusterobject.ManagedByLabelKey], clusterobject.OperatorName)

		// cleanup
		secretMgr.Clean()
	})

	t.Run("PUT Service Binding by ID 409 error (secret exists)", func(t *testing.T) {
		// given
		sbID, sbName := "318a16c3-7c80-485f-b55c-918629012c9a", "service-binding-3"
		secretName, secretNamespace := "service-binding-3-secret", "test-namespace"
		sbCreateRequest := &requests.CreateServiceBinding{
			Name:              sbName,
			ServiceInstanceID: "a7e240d6-e348-4fc0-a54c-7b7bfe9b9da6",
			SecretName:        secretName,
			SecretNamespace:   secretNamespace,
		}
		sbCreateRequestJSON, err := json.Marshal(sbCreateRequest)
		require.NoError(t, err)

		existingSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretName,
				Namespace: secretNamespace,
			},
			StringData: map[string]string{},
		}

		err = secretMgr.Create(context.TODO(), existingSecret)
		require.NoError(t, err)

		// when
		req, err := http.NewRequest(http.MethodPut, apiAddr+"/api/service-bindings/"+sbID, bytes.NewBuffer(sbCreateRequestJSON))
		require.NoError(t, err)
		resp, err := apiClient.Do(req)
		require.NoError(t, err)

		defer resp.Body.Close()
		msgBytes, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		// then
		require.Equal(t, http.StatusConflict, resp.StatusCode)
		assert.Contains(t, fmt.Sprintf("secret \"%s\" in \"%s\" namespace already exists", secretName, secretNamespace), string(msgBytes))

		// cleanup
		secretMgr.Clean()
	})
}

func defaultServiceInstances() responses.ServiceInstances {
	return responses.ServiceInstances{
		NumItems: 4,
		Items: []responses.ServiceInstance{
			{
				ID:           "f9ffbaa4-739a-4a16-ad02-6f2f17a830c5",
				Name:         "si-test-1",
				Namespace:    "kyma-system",
				SubaccountID: "a4bdee5b-2bc4-4a44-915b-196ae18c7f29",
				ClusterID:    "59c7efc0-d6bc-4d07-87cf-9bd049534afe",
			},
			{
				ID:           "df28885c-7c5f-46f0-bb75-0ae2dc85ac41",
				Name:         "si-test-2",
				Namespace:    "kyma-system",
				SubaccountID: "5ef574ba-5fb3-493f-839c-48b787f2b710",
				ClusterID:    "5dc40d3c-1839-4173-9743-d5b4f36d9d7b",
			},
			{
				ID:           "a7e240d6-e348-4fc0-a54c-7b7bfe9b9da6",
				Name:         "si-test-3",
				Namespace:    "kyma-system",
				SubaccountID: "73b7f0df-6376-4115-8e45-a0e005c0f5d2",
				ClusterID:    "4f6ee6a5-9c28-4e50-8b91-708345e1b607",
			},
			{
				ID:           "c7a604e8-f289-4f61-841f-c6519db8daf2",
				Name:         "si-test-4",
				Namespace:    "kyma-system",
				SubaccountID: "ad4e88f7-e9cc-4346-944a-d9e0dc42a038",
				ClusterID:    "8e0b4ad1-4fa0-4f7f-a6a7-3db2ac0779e2",
			},
		},
	}
}

func getServiceInstanceByID(serviceInstances responses.ServiceInstances, serviceInstanceID string) responses.ServiceInstance {
	for _, si := range serviceInstances.Items {
		if si.ID == serviceInstanceID {
			return si
		}
	}
	return responses.ServiceInstance{}
}

func defaultServiceBindings() responses.ServiceBindings {
	return responses.ServiceBindings{
		NumItems: 4,
		Items: []responses.ServiceBinding{
			{
				ID:          "550e8400-e29b-41d4-a716-446655440003",
				Name:        "service-binding",
				Credentials: map[string]interface{}{"username": "user1", "password": "pass1"},
			},
			{
				ID:          "9e420bca-4cf2-4858-ade2-e5ef23cd756f",
				Name:        "service-binding-2",
				Credentials: map[string]interface{}{"username": "user2", "password": "pass2"},
			},
			{
				ID:          "318a16c3-7c80-485f-b55c-918629012c9a",
				Name:        "service-binding-3",
				Credentials: map[string]interface{}{"username": "user3", "password": "pass3"},
			},
			{
				ID:          "8e97d56b-9fc1-43db-9d2e-e52f8ce91046",
				Name:        "service-binding-4",
				Credentials: map[string]interface{}{"username": "user4", "password": "pass4"},
			},
		},
	}
}

func defaultServiceBindingsWithSecrets() responses.ServiceBindings {
	return responses.ServiceBindings{
		NumItems: 4,
		Items: []responses.ServiceBinding{
			{
				ID:              "550e8400-e29b-41d4-a716-446655440003",
				Name:            "service-binding",
				Credentials:     map[string]interface{}{"username": "user1", "password": "pass1"},
				SecretName:      "550e8400-e29b-41d4-a716-446655440003-secret",
				SecretNamespace: "default",
			},
			{
				ID:              "9e420bca-4cf2-4858-ade2-e5ef23cd756f",
				Name:            "service-binding-2",
				Credentials:     map[string]interface{}{"username": "user2", "password": "pass2"},
				SecretName:      "9e420bca-4cf2-4858-ade2-e5ef23cd756f-secret",
				SecretNamespace: "kyma-system",
			},
			{
				ID:          "318a16c3-7c80-485f-b55c-918629012c9a",
				Name:        "service-binding-3",
				Credentials: map[string]interface{}{"username": "user3", "password": "pass3"},
			},
			{
				ID:          "8e97d56b-9fc1-43db-9d2e-e52f8ce91046",
				Name:        "service-binding-4",
				Credentials: map[string]interface{}{"username": "user4", "password": "pass4"},
			},
		},
	}
}

func getServiceBindingByID(serviceBinding responses.ServiceBindings, serviceBindingID string) responses.ServiceBinding {
	for _, sb := range serviceBinding.Items {
		if sb.ID == serviceBindingID {
			return sb
		}
	}
	return responses.ServiceBinding{}
}

func (m *MockSMClient) SetForGivenSecret(ctx context.Context, name, namespace string) error {
	if name == "" || namespace == "" {
		return types.NewServiceManagerClientError("no namespace or name set", http.StatusInternalServerError)
	}
	return nil
}

func (m *MockSMClient) ServiceInstances() (*types.ServiceInstances, error) {
	return m.fakeSMClient.ServiceInstances()
}

func (m *MockSMClient) CreateServiceInstance(si *types.ServiceInstance) (*types.ServiceInstance, error) {
	return m.fakeSMClient.CreateServiceInstance(si)
}

func (m *MockSMClient) ServiceOfferingDetails(id string) (*types.ServiceOfferingDetails, error) {
	return m.fakeSMClient.ServiceOfferingDetails(id)
}

func (m *MockSMClient) ServiceOfferings() (*types.ServiceOfferings, error) {
	return m.fakeSMClient.ServiceOfferings()
}

func (m *MockSMClient) ServiceInstanceWithPlanName(id string) (*types.ServiceInstance, error) {
	return m.fakeSMClient.ServiceInstanceWithPlanName(id)
}

func (m *MockSMClient) UpdateServiceInstance(siuReq *types.ServiceInstanceUpdateRequest) (*types.ServiceInstance, error) {
	return m.fakeSMClient.UpdateServiceInstance(siuReq)
}

func (m *MockSMClient) DeleteServiceInstance(id string) error {
	return m.fakeSMClient.DeleteServiceInstance(id)
}

func (m *MockSMClient) ServiceBindingsFor(serviceInstanceID string) (*types.ServiceBindings, error) {
	return m.fakeSMClient.ServiceBindingsFor(serviceInstanceID)
}

func (m *MockSMClient) CreateServiceBinding(sb *types.ServiceBinding) (*types.ServiceBinding, error) {
	return m.fakeSMClient.CreateServiceBinding(sb)
}

func (m *MockSMClient) ServiceBinding(id string) (*types.ServiceBinding, error) {
	return m.fakeSMClient.ServiceBinding(id)
}

func (m *MockSMClient) DeleteServiceBinding(id string) error {
	return m.fakeSMClient.DeleteServiceBinding(id)
}

func (m *MockSMClient) ServiceInstance(id string) (*types.ServiceInstance, error) {
	return m.fakeSMClient.ServiceInstance(id)
}
