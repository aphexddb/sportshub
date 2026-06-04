Implement all of these in parallel with subagents.

1. on startup, the app should have a global state (new state component for app) called "init" with a number of status checks: ports open, binaries exist, downloading binaries, etc. it should boot HTTP FIRST and using a SSE init state every 500ms, show a single loading spinner with a status message , until boot is complete. this way, the user isnt waiting for the page to start while "2026/06/04 10:02:35 [startup] Cleaning up previous sportshub/mediamtx instances and freeing ports..." runs.

2. we need to support secure RTMP links to push to, right now we can only do insecure. I know that `rtmp://601c62c19c9e.global-contribute.live-video.net/app/sk_us-east-1_fLw8XaLEWrGu_80IREiGLNSq3akIZmU1ByWczyGD0jC?gc_ext=true` worked. try `rtmps://601c62c19c9e.global-contribute.live-video.net:443/app/sk_us-east-1_fLw8XaLEWrGu_80IREiGLNSq3akIZmU1ByWczyGD0jC?gc_ext=true`

review the architecture of the go app. Using DoD (data oriented design) best practices, and Rob Pike golang design patterns: refactor the app structure. Instead of a MediaMTX binary, use an isolated native MediaMTX component. For other isolated components (such as camera state machine, etc), add tests. This will need to be compiled on windows and linux and osx, and work on arm and x64. we need to a avoid a giant go program. use many subagents to do this work.


rtmp://601c62c19c9e.global-contribute.live-video.net/app/sk_us-east-1_fLw8XaLEWrGu_80IREiGLNSq3akIZmU1ByWczyGD0jC?gc_ext=true