package main_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/cloudfoundry/gunk/diegonats"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"

	"github.com/cloudfoundry-incubator/receptor"
	"github.com/cloudfoundry-incubator/runtime-schema/models"
	"github.com/cloudfoundry-incubator/stager/backend"
	"github.com/cloudfoundry-incubator/stager/cmd/stager/testrunner"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"
	"github.com/onsi/gomega/ghttp"
)

var _ = Describe("Stager", func() {
	var (
		stagerPort   int
		gnatsdRunner ifrit.Process
		natsClient   diegonats.NATSClient
		fakeServer   *ghttp.Server
		fakeCC       *ghttp.Server
	)

	BeforeEach(func() {
		natsPort := 4001 + GinkgoParallelNode()
		stagerPort = 8888 + GinkgoParallelNode()

		fakeServer = ghttp.NewServer()

		gnatsdRunner, natsClient = diegonats.StartGnatsd(natsPort)

		fakeCC = ghttp.NewServer()

		runner = testrunner.New(testrunner.Config{
			StagerBin:     stagerPath,
			StagerURL:     fmt.Sprintf("http://127.0.0.1:%d", stagerPort),
			NatsAddresses: []string{fmt.Sprintf("127.0.0.1:%d", natsPort)},
			DiegoAPIURL:   fakeServer.URL(),
			CCBaseURL:     fakeCC.URL(),
		})
	})

	AfterEach(func() {
		runner.Stop()
		ginkgomon.Kill(gnatsdRunner)
	})

	Context("when started", func() {
		BeforeEach(func() {
			runner.Start("--lifecycles", `{"lucid64":"lifecycle.zip"}`)
		})

		Describe("when a 'diego.staging.start' message is received", func() {
			It("desires a staging task via the API", func() {
				fakeServer.RouteToHandler("POST", "/v1/tasks", func(w http.ResponseWriter, req *http.Request) {
					var taskRequest receptor.TaskCreateRequest
					err := json.NewDecoder(req.Body).Decode(&taskRequest)
					Ω(err).ShouldNot(HaveOccurred())

					Ω(taskRequest.MemoryMB).Should(Equal(1024))
					Ω(taskRequest.DiskMB).Should(Equal(128))
					Ω(taskRequest.CompletionCallbackURL).Should(Equal(runner.Config.StagerURL))
				})

				natsClient.Publish("diego.staging.start", []byte(`{
					"app_id":"my-app-guid",
					"task_id":"my-task-guid",
					"stack":"lucid64",
					"file_descriptors":3,
					"memory_mb" : 1024,
					"disk_mb" : 128,
					"environment" : [],
					"lifecycle": "buildpack",
					"lifecycle_data": {
					  "buildpacks" : [],
					  "app_bits_download_uri":"http://example.com/app_bits"
					}
				}`))

				Eventually(fakeServer.ReceivedRequests).Should(HaveLen(1))
				Consistently(runner.Session()).ShouldNot(gexec.Exit())
			})
		})

		Describe("when a 'diego.docker.staging.start' message is received", func() {
			It("desires a staging task via the API", func() {
				fakeServer.RouteToHandler("POST", "/v1/tasks", func(w http.ResponseWriter, req *http.Request) {
					var taskRequest receptor.TaskCreateRequest
					err := json.NewDecoder(req.Body).Decode(&taskRequest)
					Ω(err).ShouldNot(HaveOccurred())

					Ω(taskRequest.MemoryMB).Should(Equal(1024))
					Ω(taskRequest.DiskMB).Should(Equal(128))
					Ω(taskRequest.CompletionCallbackURL).Should(Equal(runner.Config.StagerURL))
				})

				natsClient.Publish("diego.docker.staging.start", []byte(`{
					"app_id":"my-app-guid",
					"task_id":"my-task-guid",
					"stack":"lucid64",
					"file_descriptors":3,
					"memory_mb" : 1024,
					"disk_mb" : 128,
					"environment" : [],
					"lifecycle": "docker",
					"lifecycle_data": {
					  "docker_image":"http://docker.docker/docker"
					}
				}`))

				Eventually(fakeServer.ReceivedRequests).Should(HaveLen(1))
				Consistently(runner.Session()).ShouldNot(gexec.Exit())
			})
		})

		Describe("when a staging task completes", func() {
			var resp *http.Response

			Context("for a docker lifecycle", func() {
				BeforeEach(func() {
					fakeCC.AppendHandlers(
						ghttp.CombineHandlers(
							ghttp.VerifyRequest("POST", "/internal/staging/completed"),
							ghttp.VerifyContentType("application/json"),
							ghttp.VerifyJSON(`{
								"app_id": "app-id",
								"task_id": "task-id",
								"execution_metadata": "metadata",
								"detected_start_command": {"a": "b"}
							}`),
						),
					)

					taskJSON, err := json.Marshal(receptor.TaskResponse{
						TaskGuid: "the-task-guid",
						Action: &models.RunAction{
							Path: "ls",
						},
						Domain: backend.DockerTaskDomain,
						Annotation: `{
							"app_id": "app-id",
							"task_id": "task-id"
						}`,
						Result: `{
							"execution_metadata": "metadata",
							"detected_start_command": {"a": "b"}
						}`,
					})
					Ω(err).ShouldNot(HaveOccurred())

					resp, err = http.Post(runner.Config.StagerURL, "application/json", bytes.NewReader(taskJSON))
					Ω(err).ShouldNot(HaveOccurred())
				})

				It("POSTs to the CC that staging is complete", func() {
					Eventually(fakeCC.ReceivedRequests).Should(HaveLen(1))
				})
			})

			Context("for a buildpack lifecycle", func() {
				BeforeEach(func() {
					fakeCC.AppendHandlers(
						ghttp.CombineHandlers(
							ghttp.VerifyRequest("POST", "/internal/staging/completed"),
							ghttp.VerifyContentType("application/json"),
							ghttp.VerifyJSON(`{
								"app_id": "app-id",
								"task_id": "task-id",
								"execution_metadata": "metadata",
								"detected_start_command": {"a": "b"},
								"lifecycle_data": {
									"buildpack_key": "buildpack-key",
									"detected_buildpack": "detected-buildpack"
								}
							}`),
						),
					)

					taskJSON, err := json.Marshal(receptor.TaskResponse{
						TaskGuid: "the-task-guid",
						Action: &models.RunAction{
							Path: "ls",
						},
						Domain: backend.TraditionalTaskDomain,
						Annotation: `{
							"app_id": "app-id",
							"task_id": "task-id"
						}`,
						Result: `{
							"buildpack_key": "buildpack-key",
							"detected_buildpack": "detected-buildpack",
							"execution_metadata": "metadata",
							"detected_start_command": {"a": "b"}
						}`,
					})
					Ω(err).ShouldNot(HaveOccurred())

					resp, err = http.Post(runner.Config.StagerURL, "application/json", bytes.NewReader(taskJSON))
					Ω(err).ShouldNot(HaveOccurred())
				})

				It("POSTs to the CC that staging is complete", func() {
					Eventually(fakeCC.ReceivedRequests).Should(HaveLen(1))
				})
			})
		})
	})
})
