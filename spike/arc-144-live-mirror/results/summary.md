| run | transport | p50 ms | p90 ms | p95 ms | max ms | min ms | frames observed/sent | dropped | note |
|---|---|---|---|---|---|---|---|---|---|
| f33 | mse | 104.1 | 104.9 | 105.1 | 105.8 | 59.6 | 484/600 | 0 | fragment cadence 33 ms, push→arrival 0.45 ms, 15 re-presented |
| f33 | rtp | 36.8 | 38.2 | 38.4 | 39.1 | -0.8 | 447/600 | 72 | jitter buffer 6.73 ms, decode 1.93 ms |
| hw | mse | 76.9 | 84.2 | 88.6 | 110.3 | -31.8 | 451/600 | 7 | fragment cadence 100 ms, push→arrival 0.36 ms, 0 re-presented |
| hw | rtp | 49.2 | 66.6 | 111.0 | 138.5 | 5.5 | 460/600 | 13 | jitter buffer 9.33 ms, decode 4.27 ms |
| hw2 | mse | 80.9 | 91.2 | 91.5 | 103.0 | 55.7 | 408/600 | 0 | fragment cadence 100 ms, push→arrival 0.41 ms, 0 re-presented |
| hw2 | rtp | 46.6 | 57.7 | 57.8 | 58.5 | 3.2 | 403/600 | 3 | jitter buffer 7.24 ms, decode 1.79 ms |
| on | mse | 120.6 | 121.1 | 121.3 | 122.0 | 80.7 | 557/600 | 0 | fragment cadence 100 ms, push→arrival 0.38 ms, 47 re-presented |
| on | rtp | 20.7 | 21.2 | 21.4 | 22.0 | 2.1 | 556/600 | 4 | jitter buffer 7.65 ms, decode 1.48 ms |
| on2 | mse | 118.2 | 118.8 | 119.1 | 119.5 | 57.4 | 480/600 | 0 | fragment cadence 100 ms, push→arrival 0.35 ms, 21 re-presented |
| on2 | rtp | 18.3 | 18.9 | 19.2 | 51.5 | 2.0 | 448/600 | 51 | jitter buffer 7.59 ms, decode 1.93 ms |
| on3 | mse | 128.0 | 128.9 | 129.1 | 129.5 | 42.1 | 490/600 | 1 | fragment cadence 100 ms, push→arrival 2.32 ms, 18 re-presented |
| on3 | rtp | 28.2 | 29.0 | 29.1 | 29.5 | -11.2 | 500/600 | 3 | jitter buffer 6.78 ms, decode 1.81 ms |
| on4 | mse | 106.7 | 109.0 | 109.3 | 109.9 | 59.0 | 511/600 | 0 | fragment cadence 100 ms, push→arrival 0.19 ms, 27 re-presented |
| on4 | rtp | 40.1 | 42.3 | 42.6 | 43.2 | 5.3 | 510/600 | 15 | jitter buffer 7.38 ms, decode 2.26 ms |

MSE minus RTP, same run (p50, ms):
  f33    rtp 36.8  mse 104.1  delta 67.3
  hw     rtp 49.2  mse 76.9  delta 27.7
  hw2    rtp 46.6  mse 80.9  delta 34.3
  on     rtp 20.7  mse 120.6  delta 99.9
  on2    rtp 18.3  mse 118.2  delta 99.9
  on3    rtp 28.2  mse 128.0  delta 99.8
  on4    rtp 40.1  mse 106.7  delta 66.6

browser decode facts (report-on.json):
  user agent: Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) HeadlessChrome/152.0.0.0 Safari/537.36
  MediaSource.isTypeSupported(avc): true
  decodingInfoWebRTC: supported=true smooth=true powerEfficient=true
  decodingInfoMediaSource: supported=true smooth=true powerEfficient=true
  VideoDecoder.isConfigSupported videoDecoder: true
  VideoDecoder.isConfigSupported videoDecoderPreferHardware: true
  negotiated RTP codec: video/VP8 clockRate 90000 fmtp %!q(<nil>)
  RTP video element: 1280x720, totalVideoFrames 600, dropped 4, corrupted 0
  inbound-rtp: framesDecoded 600, framesDropped 0, packetsLost 0, jitter 0, bytes 323787
