package rtsp

import (
	"crypto/tls"
	"os"
	"testing"
	"time"

	"github.com/bluenviron/gortsplib/v4"
	"github.com/bluenviron/gortsplib/v4/pkg/auth"
	"github.com/bluenviron/gortsplib/v4/pkg/base"
	"github.com/bluenviron/gortsplib/v4/pkg/description"
	"github.com/bluenviron/gortsplib/v4/pkg/format"
	"github.com/pion/rtp"
	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/test"
)

type testServer struct {
	onDescribe func(*gortsplib.ServerHandlerOnDescribeCtx) (*base.Response, *gortsplib.ServerStream, error)
	onSetup    func(*gortsplib.ServerHandlerOnSetupCtx) (*base.Response, *gortsplib.ServerStream, error)
	onPlay     func(*gortsplib.ServerHandlerOnPlayCtx) (*base.Response, error)
}

func (sh *testServer) OnDescribe(ctx *gortsplib.ServerHandlerOnDescribeCtx,
) (*base.Response, *gortsplib.ServerStream, error) {
	return sh.onDescribe(ctx)
}

func (sh *testServer) OnSetup(ctx *gortsplib.ServerHandlerOnSetupCtx) (*base.Response, *gortsplib.ServerStream, error) {
	return sh.onSetup(ctx)
}

func (sh *testServer) OnPlay(ctx *gortsplib.ServerHandlerOnPlayCtx) (*base.Response, error) {
	return sh.onPlay(ctx)
}

var testMediaH264 = &description.Media{
	Type:    description.MediaTypeVideo,
	Formats: []format.Format{test.FormatH264},
}

func TestSource(t *testing.T) {
	for _, source := range []string{
		"udp",
		"tcp",
		"tls",
	} {
		t.Run(source, func(t *testing.T) {
			var stream *gortsplib.ServerStream

			nonce, err := auth.GenerateNonce()
			require.NoError(t, err)

			s := gortsplib.Server{
				Handler: &testServer{
					onDescribe: func(ctx *gortsplib.ServerHandlerOnDescribeCtx,
					) (*base.Response, *gortsplib.ServerStream, error) {
						err := auth.Validate(ctx.Request, "testuser", "testpass", nil, nil, "IPCAM", nonce)
						if err != nil {
							return &base.Response{ //nolint:nilerr
								StatusCode: base.StatusUnauthorized,
								Header: base.Header{
									"WWW-Authenticate": auth.GenerateWWWAuthenticate(nil, "IPCAM", nonce),
								},
							}, nil, nil
						}

						return &base.Response{
							StatusCode: base.StatusOK,
						}, stream, nil
					},
					onSetup: func(ctx *gortsplib.ServerHandlerOnSetupCtx) (*base.Response, *gortsplib.ServerStream, error) {
						return &base.Response{
							StatusCode: base.StatusOK,
						}, stream, nil
					},
					onPlay: func(ctx *gortsplib.ServerHandlerOnPlayCtx) (*base.Response, error) {
						go func() {
							time.Sleep(100 * time.Millisecond)
							err := stream.WritePacketRTP(testMediaH264, &rtp.Packet{
								Header: rtp.Header{
									Version:        0x02,
									PayloadType:    96,
									SequenceNumber: 57899,
									Timestamp:      345234345,
									SSRC:           978651231,
									Marker:         true,
								},
								Payload: []byte{5, 1, 2, 3, 4},
							})
							require.NoError(t, err)
						}()

						return &base.Response{
							StatusCode: base.StatusOK,
						}, nil
					},
				},
				RTSPAddress: "127.0.0.1:8555",
			}

			switch source {
			case "udp":
				s.UDPRTPAddress = "127.0.0.1:8002"
				s.UDPRTCPAddress = "127.0.0.1:8003"

			case "tls":
				serverCertFpath, err := test.CreateTempFile(test.TLSCertPub)
				require.NoError(t, err)
				defer os.Remove(serverCertFpath)

				serverKeyFpath, err := test.CreateTempFile(test.TLSCertKey)
				require.NoError(t, err)
				defer os.Remove(serverKeyFpath)

				cert, err := tls.LoadX509KeyPair(serverCertFpath, serverKeyFpath)
				require.NoError(t, err)

				s.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}}
			}

			err = s.Start()
			require.NoError(t, err)
			defer s.Close()

			stream = gortsplib.NewServerStream(&s, &description.Session{Medias: []*description.Media{testMediaH264}})
			defer stream.Close()

			var te *test.SourceTester

			if source != "tls" {
				var sp conf.RTSPTransport
				sp.UnmarshalJSON([]byte(`"` + source + `"`)) //nolint:errcheck

				te = test.NewSourceTester(
					func(p defs.StaticSourceParent) defs.StaticSource {
						return &Source{
							ResolvedSource: "rtsp://testuser:testpass@localhost:8555/teststream",
							ReadTimeout:    conf.StringDuration(10 * time.Second),
							WriteTimeout:   conf.StringDuration(10 * time.Second),
							WriteQueueSize: 2048,
							Parent:         p,
						}
					},
					&conf.Path{
						RTSPTransport: sp,
					},
				)
			} else {
				te = test.NewSourceTester(
					func(p defs.StaticSourceParent) defs.StaticSource {
						return &Source{
							ResolvedSource: "rtsps://testuser:testpass@localhost:8555/teststream",
							ReadTimeout:    conf.StringDuration(10 * time.Second),
							WriteTimeout:   conf.StringDuration(10 * time.Second),
							WriteQueueSize: 2048,
							Parent:         p,
						}
					},
					&conf.Path{
						SourceFingerprint: "33949E05FFFB5FF3E8AA16F8213A6251B4D9363804BA53233C4DA9A46D6F2739",
					},
				)
			}

			defer te.Close()

			<-te.Unit
		})
	}
}

func TestRTSPSourceNoPassword(t *testing.T) {
	var stream *gortsplib.ServerStream

	nonce, err := auth.GenerateNonce()
	require.NoError(t, err)

	s := gortsplib.Server{
		Handler: &testServer{
			onDescribe: func(ctx *gortsplib.ServerHandlerOnDescribeCtx) (*base.Response, *gortsplib.ServerStream, error) {
				err := auth.Validate(ctx.Request, "testuser", "", nil, nil, "IPCAM", nonce)
				if err != nil {
					return &base.Response{ //nolint:nilerr
						StatusCode: base.StatusUnauthorized,
						Header: base.Header{
							"WWW-Authenticate": auth.GenerateWWWAuthenticate(nil, "IPCAM", nonce),
						},
					}, nil, nil
				}

				return &base.Response{
					StatusCode: base.StatusOK,
				}, stream, nil
			},
			onSetup: func(ctx *gortsplib.ServerHandlerOnSetupCtx) (*base.Response, *gortsplib.ServerStream, error) {
				go func() {
					time.Sleep(100 * time.Millisecond)
					err := stream.WritePacketRTP(testMediaH264, &rtp.Packet{
						Header: rtp.Header{
							Version:        0x02,
							PayloadType:    96,
							SequenceNumber: 57899,
							Timestamp:      345234345,
							SSRC:           978651231,
							Marker:         true,
						},
						Payload: []byte{5, 1, 2, 3, 4},
					})
					require.NoError(t, err)
				}()

				return &base.Response{
					StatusCode: base.StatusOK,
				}, stream, nil
			},
			onPlay: func(ctx *gortsplib.ServerHandlerOnPlayCtx) (*base.Response, error) {
				return &base.Response{
					StatusCode: base.StatusOK,
				}, nil
			},
		},
		RTSPAddress: "127.0.0.1:8555",
	}

	err = s.Start()
	require.NoError(t, err)
	defer s.Close()

	stream = gortsplib.NewServerStream(&s, &description.Session{Medias: []*description.Media{testMediaH264}})
	defer stream.Close()

	var sp conf.RTSPTransport
	sp.UnmarshalJSON([]byte(`"tcp"`)) //nolint:errcheck

	te := test.NewSourceTester(
		func(p defs.StaticSourceParent) defs.StaticSource {
			return &Source{
				ResolvedSource: "rtsp://testuser:@127.0.0.1:8555/teststream",
				ReadTimeout:    conf.StringDuration(10 * time.Second),
				WriteTimeout:   conf.StringDuration(10 * time.Second),
				WriteQueueSize: 2048,
				Parent:         p,
			}
		},
		&conf.Path{
			RTSPTransport: sp,
		},
	)
	defer te.Close()

	<-te.Unit
}

func TestRTSPSourceRange(t *testing.T) {
	for _, ca := range []string{"clock", "npt", "smpte"} {
		t.Run(ca, func(t *testing.T) {
			var stream *gortsplib.ServerStream

			s := gortsplib.Server{
				Handler: &testServer{
					onDescribe: func(ctx *gortsplib.ServerHandlerOnDescribeCtx) (*base.Response, *gortsplib.ServerStream, error) {
						return &base.Response{
							StatusCode: base.StatusOK,
						}, stream, nil
					},
					onSetup: func(ctx *gortsplib.ServerHandlerOnSetupCtx) (*base.Response, *gortsplib.ServerStream, error) {
						return &base.Response{
							StatusCode: base.StatusOK,
						}, stream, nil
					},
					onPlay: func(ctx *gortsplib.ServerHandlerOnPlayCtx) (*base.Response, error) {
						switch ca {
						case "clock":
							require.Equal(t, base.HeaderValue{"clock=20230812T120000Z-"}, ctx.Request.Header["Range"])

						case "npt":
							require.Equal(t, base.HeaderValue{"npt=0.35-"}, ctx.Request.Header["Range"])

						case "smpte":
							require.Equal(t, base.HeaderValue{"smpte=0:02:10-"}, ctx.Request.Header["Range"])
						}

						go func() {
							time.Sleep(100 * time.Millisecond)
							err := stream.WritePacketRTP(testMediaH264, &rtp.Packet{
								Header: rtp.Header{
									Version:        0x02,
									PayloadType:    96,
									SequenceNumber: 57899,
									Timestamp:      345234345,
									SSRC:           978651231,
									Marker:         true,
								},
								Payload: []byte{5, 1, 2, 3, 4},
							})
							require.NoError(t, err)
						}()

						return &base.Response{
							StatusCode: base.StatusOK,
						}, nil
					},
				},
				RTSPAddress: "127.0.0.1:8555",
			}

			err := s.Start()
			require.NoError(t, err)
			defer s.Close()

			stream = gortsplib.NewServerStream(&s, &description.Session{Medias: []*description.Media{testMediaH264}})
			defer stream.Close()

			cnf := &conf.Path{}

			switch ca {
			case "clock":
				cnf.RTSPRangeType = conf.RTSPRangeTypeClock
				cnf.RTSPRangeStart = "20230812T120000Z"

			case "npt":
				cnf.RTSPRangeType = conf.RTSPRangeTypeNPT
				cnf.RTSPRangeStart = "350ms"

			case "smpte":
				cnf.RTSPRangeType = conf.RTSPRangeTypeSMPTE
				cnf.RTSPRangeStart = "130s"
			}

			te := test.NewSourceTester(
				func(p defs.StaticSourceParent) defs.StaticSource {
					return &Source{
						ResolvedSource: "rtsp://127.0.0.1:8555/teststream",
						ReadTimeout:    conf.StringDuration(10 * time.Second),
						WriteTimeout:   conf.StringDuration(10 * time.Second),
						WriteQueueSize: 2048,
						Parent:         p,
					}
				},
				cnf,
			)
			defer te.Close()

			<-te.Unit
		})
	}
}
