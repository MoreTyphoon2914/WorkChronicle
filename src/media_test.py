import asyncio

from winrt.windows.media.control import (
    GlobalSystemMediaTransportControlsSessionManager,
)


async def main():
    manager = await GlobalSystemMediaTransportControlsSessionManager.request_async()

    sessions = manager.get_sessions()

    print(f"Sessions found: {len(sessions)}")
    print()

    for session in sessions:
        playback = session.get_playback_info()

        print("App:", session.source_app_user_model_id)
        print("Playback status:", playback.playback_status)
        print("-" * 50)


asyncio.run(main())