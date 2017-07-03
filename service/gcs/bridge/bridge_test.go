package bridge

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	oci "github.com/opencontainers/runtime-spec/specs-go"

	"github.com/Microsoft/opengcs/service/gcs/core/mockcore"
	"github.com/Microsoft/opengcs/service/gcs/oslayer"
	"github.com/Microsoft/opengcs/service/gcs/oslayer/mockos"
	"github.com/Microsoft/opengcs/service/gcs/prot"
	"github.com/Microsoft/opengcs/service/gcs/runtime"
	"github.com/Microsoft/opengcs/service/gcs/transport"
)

const (
	testTimeout = 5
)

var _ = Describe("Bridge", func() {
	var (
		connChannel    chan *transport.MockConnection
		tport          *transport.MockTransport
		coreint        *mockcore.MockCore
		commandConn    *transport.MockConnection
		messageType    prot.MessageIdentifier
		message        interface{}
		responseHeader *prot.MessageHeader
		responseString string
		responseBase   *prot.MessageResponseBase

		containerID string
		processID   uint32
		activityID  string
	)

	BeforeEach(func() {
		// Buffer connChannel so that the bridge doesn't block if we don't read
		// from the channel on the test side.
		connChannel = make(chan *transport.MockConnection, 16)
		tport = &transport.MockTransport{Channel: connChannel}
		coreint = &mockcore.MockCore{}

		containerID = "01234567-89ab-cdef-0123-456789abcdef"
		processID = 101
		activityID = "00000000-0000-0000-0000-000000000000"
	})
	// Enforce a timeout on all communications with the bridge, so that
	// situations like infinite loops or infinite blocks on the Connection will
	// fail after `testTimeout` seconds have passed.
	JustBeforeEach(func(done Done) {
		defer close(done)

		b := NewBridge(tport, coreint, false)
		go func() {
			defer GinkgoRecover()
			b.CommandLoop()
		}()
		commandConn = <-connChannel
		Expect(commandConn).NotTo(BeNil())
		messageBytes, err := json.Marshal(message)
		Expect(err).NotTo(HaveOccurred())
		messageString := string(messageBytes)

		err = serverSendString(commandConn, messageType, 0, messageString)
		Expect(err).NotTo(HaveOccurred())
		responseString, responseHeader, err = serverReadString(commandConn)
		Expect(err).NotTo(HaveOccurred())
	}, testTimeout)
	AfterEach(func() {
		close(connChannel)
		commandConn.Close()
	})

	AssertNoResponseErrors := func() {
		It("should not respond with a GCS error", func() {
			Expect(responseBase).NotTo(BeNil())
			Expect(responseBase.ErrorRecords).To(BeEmpty())
			Expect(responseBase.Result).To(BeZero())
		})
	}
	AssertResponseErrors := func(errorText string) {
		It("should respond with a GCS error", func() {
			Expect(responseBase.ErrorRecords).NotTo(BeEmpty())
			Expect(responseBase.ErrorRecords[0].Message).To(ContainSubstring(errorText))
			Expect(responseBase.Result).NotTo(BeZero())
		})
	}
	AssertActivityIDCorrect := func() {
		It("should respond with the correct activity ID", func() {
			Expect(responseBase.ActivityId).To(Equal(activityID))
		})
	}

	Describe("calling createContainer", func() {
		var (
			response       prot.ContainerCreateResponse
			createCallArgs mockcore.CreateContainerCall
			settings       prot.VmHostedContainerSettings
		)
		BeforeEach(func() {
			messageType = prot.ComputeSystemCreate_v1
		})
		JustBeforeEach(func() {
			err := json.Unmarshal([]byte(responseString), &response)
			Expect(err).NotTo(HaveOccurred())
			responseBase = response.MessageResponseBase
			createCallArgs = coreint.LastCreateContainer
		})
		Context("the message is normal ASCII", func() {
			BeforeEach(func() {
				settings = prot.VmHostedContainerSettings{
					Layers:          []prot.Layer{prot.Layer{Path: "0"}, prot.Layer{Path: "1"}, prot.Layer{Path: "2"}},
					SandboxDataPath: "3",
					MappedVirtualDisks: []prot.MappedVirtualDisk{
						prot.MappedVirtualDisk{
							ContainerPath:     "/path/inside/container",
							Lun:               4,
							CreateInUtilityVM: true,
							ReadOnly:          false,
						},
					},
					NetworkAdapters: []prot.NetworkAdapter{
						prot.NetworkAdapter{
							AdapterInstanceId:  "00000000-0000-0000-0000-000000000000",
							FirewallEnabled:    false,
							NatEnabled:         true,
							AllocatedIpAddress: "192.168.0.0",
							HostIpAddress:      "192.168.0.1",
							HostIpPrefixLength: 16,
							HostDnsServerList:  "0.0.0.0 1.1.1.1 8.8.8.8",
							HostDnsSuffix:      "microsoft.com",
							EnableLowMetric:    true,
						},
					},
				}
				settingsBytes, err := json.Marshal(settings)
				Expect(err).NotTo(HaveOccurred())
				message = prot.ContainerCreate{
					MessageBase: &prot.MessageBase{
						ContainerId: containerID,
						ActivityId:  activityID,
					},
					ContainerConfig: string(settingsBytes),
					SupportedVersions: prot.ProtocolSupport{
						MinimumVersion:         "V3",
						MaximumVersion:         "V3",
						MinimumProtocolVersion: prot.PV_V3,
						MaximumProtocolVersion: prot.PV_V3,
					},
				}
			})
			AssertNoResponseErrors()
			AssertActivityIDCorrect()
			It("should respond with the correct values", func() {
				Expect(response.SelectedVersion).To(BeEmpty())
				Expect(response.SelectedProtocolVersion).To(Equal(uint32(prot.PV_V3)))
			})
			It("should have received the correct values", func() {
				Expect(createCallArgs.ID).To(Equal(containerID))
				Expect(createCallArgs.Settings).To(Equal(settings))
			})
			Describe("sending the exit notification", func() {
				var (
					notification     prot.ContainerNotification
					registerCallArgs mockcore.RegisterContainerExitHookCall
				)
				JustBeforeEach(func(done Done) {
					defer close(done)

					registerCallArgs = coreint.LastRegisterContainerExitHook
					go func() {
						defer GinkgoRecover()
						registerCallArgs.ExitHook(mockos.NewProcessExitState(102))
					}()
					notificationString, _, err := serverReadString(commandConn)
					Expect(err).NotTo(HaveOccurred())
					err = json.Unmarshal([]byte(notificationString), &notification)
					Expect(err).NotTo(HaveOccurred())
				}, testTimeout)
				It("should respond with the correct values", func() {
					Expect(notification.ContainerId).To(Equal(containerID))
					Expect(notification.ActivityId).To(Equal(activityID))
					Expect(notification.Type).To(Equal(prot.NT_UnexpectedExit))
					Expect(notification.Operation).To(Equal(prot.AO_None))
					Expect(notification.Result).To(Equal(int32(102)))
					Expect(notification.ResultInfo).To(BeEmpty())
				})
			})
		})
	})

	Describe("calling execProcess", func() {
		var (
			response      prot.ContainerExecuteProcessResponse
			callArgs      mockcore.ExecProcessCall
			stdioSettings prot.ExecuteProcessVsockStdioRelaySettings
			params        prot.ProcessParameters
		)
		BeforeEach(func() {
			messageType = prot.ComputeSystemExecuteProcess_v1
		})
		JustBeforeEach(func() {
			err := json.Unmarshal([]byte(responseString), &response)
			Expect(err).NotTo(HaveOccurred())
			responseBase = response.MessageResponseBase
			callArgs = coreint.LastExecProcess
		})
		for _, createdPipes := range [][]bool{
			[]bool{true, true, true},
			[]bool{false, true, true},
			[]bool{true, false, true},
			[]bool{true, true, false},
			[]bool{false, false, true},
			[]bool{true, false, false},
			[]bool{false, true, false},
			[]bool{false, false, false},
		} {
			Context(fmt.Sprintf("CreateStdInPipe: %t, CreateStdOutPipe: %t, CreateStdErrPipe: %t", createdPipes[0], createdPipes[1], createdPipes[2]), func() {
				BeforeEach(func() {
					timeout := 100
					params = prot.ProcessParameters{
						CommandLine:      "sh -c testexe",
						WorkingDirectory: "/bin",
						Environment: map[string]string{
							"PATH": "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
							"TERM": "xterm",
						},
						EmulateConsole:   true,
						CreateStdInPipe:  true,
						CreateStdOutPipe: true,
						CreateStdErrPipe: true,
						IsExternal:       false,
						OCISpecification: oci.Spec{
							Version:  "1.0.0-rc5-dev",
							Platform: oci.Platform{OS: "linux", Arch: "amd64"},
							Process: &oci.Process{
								Terminal:        true,
								User:            oci.User{UID: 1001, GID: 1001, AdditionalGids: []uint32{0, 1, 2}},
								Args:            []string{"sh", "-c", "testexe"},
								Env:             []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "TERM=xterm"},
								Cwd:             "/bin",
								Capabilities:    &oci.LinuxCapabilities{Bounding: []string{"CAP_AUDIT_WRITE", "CAP_KILL", "CAP_NET_BIND_SERVICE"}},
								Rlimits:         []oci.LinuxRlimit{oci.LinuxRlimit{Type: "RLIMIT_NOFILE", Hard: 1024, Soft: 1024}},
								NoNewPrivileges: true,
								ApparmorProfile: "testing",
								SelinuxLabel:    "testing",
							},
							Root:     oci.Root{Path: "rootfs", Readonly: true},
							Hostname: "test",
							Mounts:   []oci.Mount{oci.Mount{Destination: "/dev", Type: "tmpfs", Source: "tmpfs", Options: []string{"nosuid"}}},
							Hooks: &oci.Hooks{
								Prestart:  []oci.Hook{oci.Hook{Path: "/bin/hook", Args: []string{"hookarg"}, Env: []string{"TERM=xterm"}, Timeout: &timeout}},
								Poststart: []oci.Hook{oci.Hook{Path: "/bin/hook", Args: []string{"hookarg"}, Env: []string{"TERM=xterm"}, Timeout: &timeout}},
								Poststop:  []oci.Hook{oci.Hook{Path: "/bin/hook", Args: []string{"hookarg"}, Env: []string{"TERM=xterm"}, Timeout: &timeout}},
							},
							Annotations: map[string]string{"t": "esting"},
							Linux: &oci.Linux{
								UIDMappings: []oci.LinuxIDMapping{oci.LinuxIDMapping{HostID: 1001, ContainerID: 1005, Size: 128}},
								GIDMappings: []oci.LinuxIDMapping{oci.LinuxIDMapping{HostID: 1001, ContainerID: 1005, Size: 128}},
								Sysctl:      map[string]string{"test": "ing"},
								// TODO: Add Resources field?
								CgroupsPath: "/testing/path",
								Namespaces:  []oci.LinuxNamespace{oci.LinuxNamespace{Type: "network", Path: "/etc/netns/testns"}},
								// TODO: Add Devices field?
								// TODO: Add Seccomp field?
								RootfsPropagation: "testmode",
								MaskedPaths:       []string{"/test/path", "/other/test/path"},
								ReadonlyPaths:     []string{"/testing/", "/test/path"},
								MountLabel:        "label",
							},
						},
					}
					paramsBytes, err := json.Marshal(params)
					Expect(err).NotTo(HaveOccurred())
					stdioSettings = prot.ExecuteProcessVsockStdioRelaySettings{
						StdIn:  1,
						StdOut: 2,
						StdErr: 3,
					}
					message = prot.ContainerExecuteProcess{
						MessageBase: &prot.MessageBase{
							ContainerId: containerID,
							ActivityId:  activityID,
						},
						Settings: prot.ExecuteProcessSettings{
							ProcessParameters:       string(paramsBytes),
							VsockStdioRelaySettings: stdioSettings,
						},
					}
				})
				AssertNoResponseErrors()
				AssertActivityIDCorrect()
				It("should respond with the correct values", func() {
					Expect(response.ProcessId).To(Equal(uint32(101)))
				})
				It("should have received the correct values", func() {
					Expect(callArgs.ID).To(Equal(containerID))
					Expect(callArgs.Params).To(Equal(params))
					// TODO: How to test this? Do we want to?
					//Expect(callArgs.StdioSet).To(Equal(stdioSet))
				})
			})
		}
	})

	Describe("calling runExternalProcess", func() {
		var (
			response      prot.ContainerExecuteProcessResponse
			callArgs      mockcore.RunExternalProcessCall
			stdioSettings prot.ExecuteProcessVsockStdioRelaySettings
			params        prot.ProcessParameters
		)
		BeforeEach(func() {
			messageType = prot.ComputeSystemExecuteProcess_v1
		})
		JustBeforeEach(func() {
			err := json.Unmarshal([]byte(responseString), &response)
			Expect(err).NotTo(HaveOccurred())
			responseBase = response.MessageResponseBase
			callArgs = coreint.LastRunExternalProcess
		})
		for _, createdPipes := range [][]bool{
			[]bool{true, true, true},
			[]bool{false, true, true},
			[]bool{true, false, true},
			[]bool{true, true, false},
			[]bool{false, false, true},
			[]bool{true, false, false},
			[]bool{false, true, false},
			[]bool{false, false, false},
		} {
			Context(fmt.Sprintf("CreateStdInPipe: %t, CreateStdOutPipe: %t, CreateStdErrPipe: %t", createdPipes[0], createdPipes[1], createdPipes[2]), func() {
				BeforeEach(func() {
					params = prot.ProcessParameters{
						CommandLine:      "sh -c /bin/testexe",
						WorkingDirectory: "/bin",
						Environment: map[string]string{
							"PATH": "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
							"TERM": "xterm",
						},
						EmulateConsole:   true,
						CreateStdInPipe:  createdPipes[0],
						CreateStdOutPipe: createdPipes[1],
						CreateStdErrPipe: createdPipes[2],
						IsExternal:       true,
					}
					paramsBytes, err := json.Marshal(params)
					Expect(err).NotTo(HaveOccurred())
					stdioSettings = prot.ExecuteProcessVsockStdioRelaySettings{
						StdIn:  1,
						StdOut: 2,
						StdErr: 3,
					}
					message = prot.ContainerExecuteProcess{
						MessageBase: &prot.MessageBase{
							ContainerId: containerID,
							ActivityId:  activityID,
						},
						Settings: prot.ExecuteProcessSettings{
							ProcessParameters:       string(paramsBytes),
							VsockStdioRelaySettings: stdioSettings,
						},
					}
				})
				AssertNoResponseErrors()
				AssertActivityIDCorrect()
				It("should respond with the correct values", func() {
					Expect(response.ProcessId).To(Equal(uint32(101)))
				})
				It("should have received the correct values", func() {
					Expect(callArgs.Params).To(Equal(params))
					// TODO: How to test this? Do we want to?
					//Expect(callArgs.StdioSet).To(Equal(stdioSet))
				})
			})
		}
	})

	Describe("calling killContainer", func() {
		var (
			response prot.MessageResponseBase
			callArgs mockcore.SignalContainerCall
		)
		BeforeEach(func() {
			messageType = prot.ComputeSystemShutdownForced_v1
		})
		JustBeforeEach(func() {
			err := json.Unmarshal([]byte(responseString), &response)
			Expect(err).NotTo(HaveOccurred())
			responseBase = &response
			callArgs = coreint.LastSignalContainer
		})
		Context("the message is normal ASCII", func() {
			BeforeEach(func() {
				message = prot.MessageBase{
					ContainerId: containerID,
					ActivityId:  activityID,
				}
			})
			AssertNoResponseErrors()
			AssertActivityIDCorrect()
			It("should receive the correct values", func() {
				Expect(callArgs.ID).To(Equal(containerID))
				Expect(callArgs.Signal).To(Equal(oslayer.SIGKILL))
			})
		})
	})

	Describe("calling shutdownContainer", func() {
		var (
			response prot.MessageResponseBase
			callArgs mockcore.SignalContainerCall
		)
		BeforeEach(func() {
			messageType = prot.ComputeSystemShutdownGraceful_v1
		})
		JustBeforeEach(func() {
			err := json.Unmarshal([]byte(responseString), &response)
			Expect(err).NotTo(HaveOccurred())
			responseBase = &response
			callArgs = coreint.LastSignalContainer
		})
		Context("the message is normal ASCII", func() {
			BeforeEach(func() {
				message = prot.MessageBase{
					ContainerId: containerID,
					ActivityId:  activityID,
				}
			})
			AssertNoResponseErrors()
			AssertActivityIDCorrect()
			It("should receive the correct values", func() {
				Expect(callArgs.ID).To(Equal(containerID))
				Expect(callArgs.Signal).To(Equal(oslayer.SIGTERM))
			})
		})
	})

	Describe("calling terminateProcess", func() {
		var (
			response prot.MessageResponseBase
			callArgs mockcore.TerminateProcessCall
		)
		BeforeEach(func() {
			messageType = prot.ComputeSystemTerminateProcess_v1
		})
		JustBeforeEach(func() {
			err := json.Unmarshal([]byte(responseString), &response)
			Expect(err).NotTo(HaveOccurred())
			responseBase = &response
			callArgs = coreint.LastTerminateProcess
		})
		Context("the message is normal ASCII", func() {
			BeforeEach(func() {
				message = prot.ContainerTerminateProcess{
					MessageBase: &prot.MessageBase{
						ContainerId: containerID,
						ActivityId:  activityID,
					},
					ProcessId: processID,
				}
			})
			AssertNoResponseErrors()
			AssertActivityIDCorrect()
			It("should receive the correct values", func() {
				Expect(callArgs.Pid).To(Equal(int(processID)))
			})
		})
	})

	Describe("calling listProcesses", func() {
		var (
			response prot.ContainerGetPropertiesResponse
			callArgs mockcore.ListProcessesCall
		)
		BeforeEach(func() {
			messageType = prot.ComputeSystemGetProperties_v1
		})
		JustBeforeEach(func() {
			err := json.Unmarshal([]byte(responseString), &response)
			Expect(err).NotTo(HaveOccurred())
			responseBase = response.MessageResponseBase
			callArgs = coreint.LastListProcesses
		})
		Context("the message is normal ASCII", func() {
			BeforeEach(func() {
				message = prot.ContainerGetProperties{
					MessageBase: &prot.MessageBase{
						ContainerId: containerID,
						ActivityId:  activityID,
					},
				}
			})
			AssertNoResponseErrors()
			AssertActivityIDCorrect()
			It("should respond with the correct values", func() {
				var states []runtime.ContainerProcessState
				err := json.Unmarshal([]byte(response.Properties), &states)
				Expect(err).NotTo(HaveOccurred())
				expectedState := runtime.ContainerProcessState{
					Pid:              101,
					Command:          []string{"sh", "-c", "testexe"},
					CreatedByRuntime: true,
					IsZombie:         true,
				}
				Expect(states).To(Equal([]runtime.ContainerProcessState{expectedState}))
			})
			It("should have received the correct values", func() {
				Expect(callArgs.ID).To(Equal(containerID))
			})
		})
	})

	Describe("calling waitOnProcess", func() {
		var (
			response prot.ContainerWaitForProcessResponse
			callArgs mockcore.RegisterProcessExitHookCall
		)
		BeforeEach(func() {
			messageType = prot.ComputeSystemWaitForProcess_v1
		})
		JustBeforeEach(func() {
			err := json.Unmarshal([]byte(responseString), &response)
			Expect(err).NotTo(HaveOccurred())
			responseBase = response.MessageResponseBase
			callArgs = coreint.LastRegisterProcessExitHook
		})
		Context("the message is normal ASCII", func() {
			BeforeEach(func() {
				message = prot.ContainerWaitForProcess{
					MessageBase: &prot.MessageBase{
						ContainerId: containerID,
						ActivityId:  activityID,
					},
					ProcessId:   101,
					TimeoutInMs: 1000,
				}
			})
			AssertNoResponseErrors()
			AssertActivityIDCorrect()
			It("should respond with the correct values", func() {
				Expect(response.ExitCode).To(Equal(uint32(103)))
			})
			It("should have received the correct values", func() {
				Expect(callArgs.Pid).To(Equal(101))
			})
		})
	})

	Describe("calling resizeConsole", func() {
		var (
			response prot.MessageResponseBase
			//callArgs mockcore.ResizeConsoleCall
		)
		BeforeEach(func() {
			messageType = prot.ComputeSystemResizeConsole_v1
		})
		JustBeforeEach(func() {
			err := json.Unmarshal([]byte(responseString), &response)
			Expect(err).NotTo(HaveOccurred())
			responseBase = &response
			//callArgs = coreint.LastResizeConsole
		})
		Context("the message is normal ASCII", func() {
			BeforeEach(func() {
				message = prot.ContainerResizeConsole{
					MessageBase: &prot.MessageBase{
						ContainerId: containerID,
						ActivityId:  activityID,
					},
					ProcessId: 101,
					Height:    30,
					Width:     72,
				}
			})
			AssertNoResponseErrors()
			AssertActivityIDCorrect()
			// TODO: Add tests on callArgs when resizing the console is
			// implemented.
			It("should receive the correct values", func() {
				//e.g. Expect(callArgs.Pid).To(Equal(101))
			})
		})
	})

	Describe("calling modifySettings", func() {
		var (
			response                       prot.MessageResponseBase
			callArgs                       mockcore.ModifySettingsCall
			modificationRequest            prot.ResourceModificationRequestResponse
			defaultModificationRequest     prot.ResourceModificationRequestResponse
			unsupportedModificationRequest prot.ResourceModificationRequestResponse
		)
		BeforeEach(func() {
			messageType = prot.ComputeSystemModifySettings_v1
		})
		JustBeforeEach(func() {
			err := json.Unmarshal([]byte(responseString), &response)
			Expect(err).NotTo(HaveOccurred())
			responseBase = &response
			callArgs = coreint.LastModifySettings
		})
		Context("the message is normal ASCII", func() {
			BeforeEach(func() {
				disk := prot.MappedVirtualDisk{
					ContainerPath:     "/path/inside/container",
					Lun:               4,
					CreateInUtilityVM: true,
					ReadOnly:          false,
				}
				modificationRequest = prot.ResourceModificationRequestResponse{
					ResourceType: prot.PT_MappedVirtualDisk,
					RequestType:  prot.RT_Add,
					Settings:     prot.ResourceModificationSettings{MappedVirtualDisk: &disk},
				}
				defaultModificationRequest = prot.ResourceModificationRequestResponse{
					ResourceType: "",
					RequestType:  "",
					Settings:     prot.ResourceModificationSettings{MappedVirtualDisk: &disk},
				}
				unsupportedModificationRequest = prot.ResourceModificationRequestResponse{
					ResourceType: prot.PT_Memory,
					RequestType:  prot.RT_Add,
					Settings:     prot.ResourceModificationSettings{MappedVirtualDisk: &disk},
				}
			})
			Context("using non-empty ResourceType and RequestType", func() {
				BeforeEach(func() {
					message = prot.ContainerModifySettings{
						MessageBase: &prot.MessageBase{
							ContainerId: containerID,
							ActivityId:  activityID,
						},
						Request: modificationRequest,
					}
				})
				AssertNoResponseErrors()
				AssertActivityIDCorrect()
				It("should receive the correct values", func() {
					Expect(callArgs.ID).To(Equal(containerID))
					Expect(callArgs.Request).To(Equal(modificationRequest))
				})
			})
			Context("using empty ResourceType and RequestType", func() {
				BeforeEach(func() {
					message = prot.ContainerModifySettings{
						MessageBase: &prot.MessageBase{
							ContainerId: containerID,
							ActivityId:  activityID,
						},
						Request: defaultModificationRequest,
					}
				})
				AssertResponseErrors("Invalid ResourceType Memory")
				AssertActivityIDCorrect()
			})
			Context("using an unsupported ResourceType", func() {
				BeforeEach(func() {
					message = prot.ContainerModifySettings{
						MessageBase: &prot.MessageBase{
							ContainerId: containerID,
							ActivityId:  activityID,
						},
						Request: unsupportedModificationRequest,
					}
				})
				AssertResponseErrors("Invalid ResourceType Memory")
				AssertActivityIDCorrect()
			})
		})
	})
})

func serverSendString(conn transport.Connection, messageType prot.MessageIdentifier, messageId prot.SequenceId, str string) error {
	if err := serverSendHeader(conn, messageType, messageId, len(str)); err != nil {
		return err
	}
	if err := serverSendMessage(conn, str); err != nil {
		return err
	}
	return nil
}

func serverSendHeader(conn transport.Connection, messageType prot.MessageIdentifier, messageId prot.SequenceId, size int) error {
	header := prot.MessageHeader{}
	header.Type = messageType
	header.Id = messageId
	header.Size = uint32(size + prot.MessageHeaderSize)
	var bytesToSend bytes.Buffer
	if err := binary.Write(&bytesToSend, binary.LittleEndian, &header); err != nil {
		return err
	}
	if err := serverSendBytes(conn, bytesToSend.Bytes()); err != nil {
		return err
	}
	return nil
}

func serverSendMessage(conn transport.Connection, message string) error {
	if err := serverSendBytes(conn, []byte(message)); err != nil {
		return err
	}
	return nil
}

func serverSendBytes(conn transport.Connection, bytes []byte) error {
	numRemainingBytes := len(bytes)
	bytesToSend := bytes
	for numRemainingBytes > 0 {
		n, err := conn.Write(bytesToSend)
		if err != nil {
			return err
		}
		bytesToSend = bytesToSend[n:]
		numRemainingBytes -= n
	}
	return nil
}

func serverReadString(conn transport.Connection) (str string, header *prot.MessageHeader, err error) {
	header, err = serverReadHeader(conn)
	if err != nil {
		return "", nil, err
	}
	message, err := serverReadMessage(conn, int(header.Size))
	if err != nil {
		return "", nil, err
	}
	return message, header, nil
}

func serverReadHeader(conn transport.Connection) (*prot.MessageHeader, error) {
	headerBytes, err := serverReadBytes(conn, prot.MessageHeaderSize)
	if err != nil {
		return nil, err
	}
	buf := bytes.NewReader(headerBytes)
	header := prot.MessageHeader{}
	if err := binary.Read(buf, binary.LittleEndian, &header); err != nil {
		return nil, err
	}
	return &header, nil
}

func serverReadMessage(conn transport.Connection, messageSize int) (string, error) {
	messageBytes, err := serverReadBytes(conn, messageSize-prot.MessageHeaderSize)
	if err != nil {
		return "", err
	}
	return string(messageBytes), nil
}

func serverReadBytes(conn transport.Connection, n int) ([]byte, error) {
	numRemainingBytes := n
	returnBytes := make([]byte, 0, numRemainingBytes)
	for numRemainingBytes > 0 {
		tempBytes := make([]byte, numRemainingBytes)
		n, err := conn.Read(tempBytes)
		if err != nil {
			return nil, err
		}
		returnBytes = append(returnBytes, tempBytes[:n]...)
		numRemainingBytes -= n
	}
	return returnBytes, nil
}
