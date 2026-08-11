import os
import re
import asyncio
import aiohttp
import aiofiles

from anony import app, logger


class API:
    def __init__(
            self, api_url: str, api_key: str,
            retries: int = 10, timeout: int = 10,
        ):
        self.api_url = api_url
        self.api_key = api_key
        self.chunk_limit = 1024 * 1024
        self.dl_cache = {}
        self.v_cache = {}
        self.retries = retries
        self.dl_endp = self.api_url + "/youtube/v2/download"
        self.job_endp = self.api_url + "/youtube/jobStatus"
        self.params = {"api_key": self.api_key}
        self.timeout = aiohttp.ClientTimeout(total=timeout)
        self.session: aiohttp.ClientSession | None = None
        self.regex = re.compile(
            r"(https?://)?(www\.)?"
            r"t\.me/(c/)?"
            r"([\w\d_]+)/(\d+)"
            r"([&?][^\s]*)?"
        )

    async def get_session(self) -> None:
        self.session = aiohttp.ClientSession(timeout=self.timeout)

    async def close(self) -> None:
        if self.session:
            await self.session.close()

    async def request_download(self, video_id: str, video: bool = False) -> dict | None:
        _params = self.params.copy()
        _params["query"] = video_id
        _params["isVideo"] = str(video).lower()

        for _ in range(3):
            try:
                async with self.session.get(self.dl_endp, params=_params) as resp:
                    if resp.status != 200:
                        await asyncio.sleep(1)
                        continue

                    _data = await resp.json()
                    status = _data.get("status")

                    if status == "success" and _data.get("result", {}).get("cdn"):
                        return _data

                    if status == "queued" and _data.get("job_id"):
                        return _data

                    await asyncio.sleep(1)
            except Exception as e:
                logger.debug("request_download attempt failed: %s", e)
                await asyncio.sleep(1)

        return None

    async def get_url(self, job_id: str) -> str | None:
        _params = {"job_id": job_id}
        for attempt in range(1, self.retries + 1):
            try:
                async with self.session.get(self.job_endp, params=_params) as resp:
                    if resp.status != 200:
                        await asyncio.sleep(3)
                        continue

                    _data = await resp.json()
                    status = _data.get("status")

                    if status != "success":
                        await asyncio.sleep(3)
                        continue

                    job = _data.get("job", {})
                    if job.get("status", "") != "done":
                        await asyncio.sleep(3)
                        continue

                    cdn = job.get("result", {}).get("cdn")
                    if not cdn:
                        break

                    logger.info(f"ArcApi: Received #{attempt} [{cdn}]")
                    return cdn
            except Exception as e:
                logger.debug("get_url attempt failed for job %s: %s", job_id, e)

            await asyncio.sleep(3)
        return None

    async def save_file(self, url: str) -> str | None:
        raw_name = url.split("/")[-1]
        safe_name = os.path.basename(raw_name)
        if not safe_name or safe_name.startswith("."):
            safe_name = "download"
        fpath = "downloads/" + safe_name
        try:
            async with self.session.get(url, timeout=None) as resp:
                if resp.status != 200:
                    return None

                async with aiofiles.open(fpath, "wb") as f:
                    async for chunk in resp.content.iter_chunked(self.chunk_limit):
                        if chunk:
                            await f.write(chunk)

                return fpath
        except Exception as e:
            logger.error(f"Failed to save file from API: {e}")
        return None

    async def save_from_telegram(self, cdn: str) -> str | None:
        match = self.regex.match(cdn)
        if not match:
            return None

        username, message_id = match.group(4), int(match.group(5))
        try:
            track = await app.get_messages(chat_id=username, message_ids=message_id)
            return await track.download()
        except Exception as e:
            logger.error(f"Failed to download telegram cdn message: {e}")
            return None

    async def resolve_cdn(self, cdn: str) -> str | None:
        if self.regex.match(cdn):
            return await self.save_from_telegram(cdn)
        return await self.save_file(cdn)

    async def download(self, vid_id: str, video: bool = False) -> str | None:
        if video and vid_id in self.v_cache:
            return self.v_cache[vid_id]
        elif not video and vid_id in self.dl_cache:
            return self.dl_cache[vid_id]

        for attempt in range(2):
            resp = await self.request_download(vid_id, video)
            if not resp:
                if attempt == 0: await asyncio.sleep(2)
                continue

            if resp.get("job_id"):
                cdn = await self.get_url(resp["job_id"])
            else:
                cdn = resp.get("result", {}).get("cdn")

            if not cdn:
                if attempt == 0: await asyncio.sleep(2)
                continue

            fpath = await self.resolve_cdn(cdn)
            if not fpath:
                if attempt == 0: await asyncio.sleep(2)
                continue

            if video:
                self.v_cache[vid_id] = fpath
            else:
                self.dl_cache[vid_id] = fpath

            return fpath

        return None
