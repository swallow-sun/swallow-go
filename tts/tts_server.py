#!/usr/bin/env python3
"""
Swallow 本地 TTS 服务 - 基于 CosyVoice2.

功能:
  - 接收 POST /tts, body = {"text": "要合成的文字", "tone": "warm"}
  - 用 inference_instruct2 做语气控制 + 音色克隆
  - 参考音频做 zero-shot 音色克隆 (米彩广播剧女声)
  - 返回完整 WAV (16kHz 单声道 16-bit PCM)
  - 启动时加载模型到 GPU, 常驻内存

启动:
  python tts_server.py --model_dir D:/swallow/swallow-go/tts/models/cosyvoice2 \
                       --ref_wav D:/swallow/swallow-go/tts/data/voice_ref.wav \
                       --ref_text "喜欢一首歌。很多时候并不是真的喜欢。只是借一种方式去怀念一个人。" \
                       --port 9880
"""
import os
import sys
import io
import argparse
import logging
import time
import struct
import wave

logging.getLogger('matplotlib').setLevel(logging.WARNING)

# modelscope 离线模式: wetext 模型已缓存到 ~/.cache/modelscope, 避免每次启动都查 API (403).
# wetext 的 Normalizer 初始化时调 snapshot_download("pengzhendong/wetext") 在线查 revision,
# 国内网络/无 token 时会 403 失败. 设 MODELSCOPE_OFFLINE=1 让它直接用本地缓存.
os.environ['MODELSCOPE_OFFLINE'] = '1'


# 把 CosyVoice 仓库根目录加入 sys.path, 让 import cosyvoice 能找到.
ROOT_DIR = os.path.dirname(os.path.abspath(__file__))
COSYVOICE_ROOT = os.path.join(ROOT_DIR, 'third_party', 'CosyVoice')
sys.path.append(COSYVOICE_ROOT)
# Matcha-TTS 是 CosyVoice 的依赖, 也在 third_party 下.
sys.path.append(os.path.join(COSYVOICE_ROOT, 'third_party', 'Matcha-TTS'))


class PrefixedStderr:
    """包装 stderr, 给每一行输出加上 'LEVEL ' 前缀.

    第三方库 (ONNX Runtime, torch, modelscope 等) 直接写 stderr, 不走 Python logging,
    所以没有 LEVEL 前缀. Go 侧 pumpLogs 靠前缀分流级别, 没前缀的行只能猜.

    这个类拦截 stderr 写入, 给每行加 'LEVEL ' 前缀:
      - 含 ERROR/Traceback 的行 -> ERROR 前缀
      - 含 WARNING/Warn 的行   -> WARNING 前缀
      - 其他                   -> INFO 前缀

    这样 Go 侧 splitLogLevel 总能正确分流, 不需要猜.

    噪音过滤: torch import 时探测 torio/FFmpeg 扩展会输出噪音日志.
    Python 侧做单行过滤 (stderr), Go 侧 pumpLogs 做块级 traceback 过滤 (stdout+stderr).
    """
    def __init__(self, stream):
        self._stream = stream
        self._buf = ""

    def write(self, text):
        if not text:
            return
        self._buf += text
        while '\n' in self._buf:
            line, self._buf = self._buf.split('\n', 1)
            if line.strip():
                if self._is_noise(line):
                    continue
                self._stream.write(self._add_prefix(line) + '\n')
            else:
                self._stream.write('\n')
        self._stream.flush()

    def flush(self):
        if self._buf.strip():
            if not self._is_noise(self._buf):
                self._stream.write(self._add_prefix(self._buf) + '\n')
        self._buf = ""
        self._stream.flush()

    @staticmethod
    def _add_prefix(line):
        upper = line.upper()
        if 'ERROR' in upper or 'TRACEBACK' in upper:
            return 'ERROR ' + line
        # Python warnings.warn 输出不含 WARNING 关键词, 靠特征词识别.
        if 'WARNING' in upper or 'WARN' in upper or 'DEPRECATED' in upper:
            return 'WARNING ' + line
        return 'INFO ' + line

    # 噪音行关键词: torio/FFmpeg 加载失败, 不影响功能 (项目用 scipy 替代 torchaudio).
    NOISE_KEYWORDS = ['torio', 'ffmpeg', 'libtorio']

    def _is_noise(self, line):
        lower = line.lower()
        return any(kw in lower for kw in self.NOISE_KEYWORDS)

    def __getattr__(self, name):
        return getattr(self._stream, name)

    def __getattr__(self, name):
        return getattr(self._stream, name)


# 用 PrefixedStderr 替换 sys.stderr, 让第三方库的 stderr 输出也带 LEVEL 前缀.
# 必须在 import torch/onnxruntime 等第三方库之前替换, 否则它们可能缓存了原始 stderr.
sys.stderr = PrefixedStderr(sys.stderr)

from fastapi import FastAPI
from fastapi.responses import Response, StreamingResponse
from pydantic import BaseModel
import uvicorn
import numpy as np
import torch
from scipy.io import wavfile
from scipy.signal import resample_poly
from math import gcd

from cosyvoice.cli.cosyvoice import AutoModel
from cosyvoice.utils.file_utils import load_wav


def scipy_load_wav(path, target_sr):
    """scipy 替代 torchaudio.load + Resample, 返回 [1, T] float32 tensor."""
    sr, data = wavfile.read(path)
    # 转 float32, 归一化到 [-1, 1]
    if data.dtype == np.int16:
        data = data.astype(np.float32) / 32768.0
    elif data.dtype == np.int32:
        data = data.astype(np.float32) / 2147483648.0
    elif data.dtype == np.float32:
        pass
    elif data.dtype == np.float64:
        data = data.astype(np.float32)
    # stereo -> mono
    if data.ndim > 1:
        data = data.mean(axis=1)
    # 重采样
    if sr != target_sr:
        g = gcd(sr, target_sr)
        up = target_sr // g
        down = sr // g
        data = resample_poly(data, up, down).astype(np.float32)
    return torch.from_numpy(data).unsqueeze(0)  # [1, T]

logger = logging.getLogger('swallow-tts')
# 日志格式: "LEVEL message", 不带时间戳和 logger 名.
# Go 侧 pumpLogs 会解析级别前缀, 只提取 message 作为 msg 输出.
# 这样 Python 日志和 Go 日志格式完全一致 (Go 加时间戳和 JSON 结构).
logging.basicConfig(level=logging.INFO, format='%(levelname)s %(message)s')

# 抑制第三方库的噪音日志, 只保留 swallow-tts 自己的日志.
logging.getLogger('uvicorn').setLevel(logging.INFO)
logging.getLogger('uvicorn.access').setLevel(logging.INFO)

app = FastAPI()

# 全局变量, 启动时初始化.
cosyvoice = None
ref_wav_16k = None      # 参考音频 tensor, 16kHz
ref_text = None          # 参考音频的转录文本
sample_rate = 24000      # CosyVoice2 输出采样率, 从模型配置读


class TtsRequest(BaseModel):
    # 要合成的纯净文本 (不含 <tags>).
    text: str
    # 语气标签 (calm/warm/cheerful/... ), 空字符串表示不加语气控制.
    tone: str = ""


# 17 种语气标签 -> CosyVoice2 自然语言情感指令.
# 和 Go 侧 internal/provider/tts/tone.go 的映射表完全一致.
TONE_TO_PROMPT = {
    "calm":         "用平静的语气说",
    "warm":         "用温和的语气说",
    "cheerful":     "用愉快的语气说",
    "serious":      "用严肃的语气说",
    "concerned":    "用关切的语气说",
    "gentle":       "用轻柔的语气说",
    "energetic":    "用充满活力的语气说",
    "apologetic":   "用抱歉的语气说",
    "sad":          "用伤心的语气说",
    "frustrated":   "用无奈的语气说",
    "angry":        "用生气的语气说",
    "disappointed": "用失望的语气说",
    "coquettish":   "用撒娇的语气说",
    "wronged":      "用委屈的语气说",
    "exasperated": "用又气又急的语气说",
    "melancholy":   "用忧郁的语气说",
    "smug":         "用得意的语气说",
}

END_OF_PROMPT = "<|endofprompt|>"


def apply_tone_prefix(text: str, tone: str) -> str:
    """
    根据语气标签拼装 CosyVoice2 instruct2 指令.
    格式: "用XX的语气说<|endofprompt|>实际文本"
    和 Go 侧 tts.ApplyTonePrefix 逻辑一致.
    """
    tone = tone.strip().lower() if tone else ""
    prompt = TONE_TO_PROMPT.get(tone)
    if not prompt:
        return text  # tone 为空或不在映射表里, 不加前缀
    return f"{prompt} {END_OF_PROMPT}{text}"


def pcm16_to_wav(pcm_bytes: bytes, sr: int, channels: int = 1, bits: int = 16) -> bytes:
    """
    把裸 PCM16 字节拼上 WAV 头, 返回完整 WAV 文件字节.
    C++ 侧的 WaveOutPlayer 需要完整 WAV (含 WAV 头) 才能解析播放.
    """
    data_size = len(pcm_bytes)
    wav_buf = io.BytesIO()
    with wave.open(wav_buf, 'wb') as wf:
        wf.setnchannels(channels)
        wf.setsampwidth(bits // 8)
        wf.setframerate(sr)
        wf.writeframes(pcm_bytes)
    return wav_buf.getvalue()


@app.post("/tts")
async def tts(req: TtsRequest):
    """
    TTS 合成接口.
    请求: {"text": "你好", "tone": "warm"}
    响应: audio/wav 二进制数据.
    """
    if cosyvoice is None:
        return Response(content=b'', media_type='audio/wav', status_code=503)

    text = req.text.strip()
    if not text:
        return Response(content=b'', media_type='audio/wav', status_code=400)

    # 拼装语气指令.
    instruct_text = apply_tone_prefix(text, req.tone)
    logger.info(f"tts: text={text[:120]}, tone={req.tone}, "
                f"text_chars={len(text)}, instruct={instruct_text[:120]}")

    start = time.time()

    # 用 inference_instruct2 合成语音.
    # 参数: tts_text=要合成的文本, instruct_text=语气指令, prompt_wav=参考音频.
    # 返回 generator, 每次 yield 一个 {'tts_speech': tensor} (shape [1, T]).
    all_speech = []
    try:
        for output in cosyvoice.inference_instruct2(
            tts_text=text,
            instruct_text=instruct_text,
            prompt_wav=ref_wav_16k,
            stream=False,
        ):
            all_speech.append(output['tts_speech'])
    except Exception as e:
        import traceback
        logger.error(f"tts: inference failed: {e}\n{traceback.format_exc()}")
        return Response(content=b'', media_type='audio/wav', status_code=500)

    if not all_speech:
        logger.error("tts: inference returned empty")
        return Response(content=b'', media_type='audio/wav', status_code=500)

    # 拼接所有 chunk, 转成 PCM16.
    speech = torch.cat(all_speech, dim=1)  # [1, total_T]
    speech_np = speech.numpy().squeeze(0)  # [total_T]

    # float32 [-1, 1] -> int16 [-32768, 32767]
    pcm16 = (speech_np * 32767).clip(-32768, 32767).astype(np.int16)
    pcm_bytes = pcm16.tobytes()

    # 采样率: CosyVoice2 输出 24000 Hz, 重采样到 16000 Hz (C++ 侧 waveOut 期望 16kHz).
    if sample_rate != 16000:
        # scipy 重采样 (torchaudio 不可用).
        speech_np_f32 = speech_np.astype(np.float32) / 32767.0
        g = gcd(sample_rate, 16000)
        up = 16000 // g
        down = sample_rate // g
        speech_16k = resample_poly(speech_np_f32, up, down).astype(np.float32)
        pcm16 = (speech_16k * 32767).clip(-32768, 32767).astype(np.int16)
        pcm_bytes = pcm16.tobytes()
        output_sr = 16000
    else:
        output_sr = sample_rate

    wav_bytes = pcm16_to_wav(pcm_bytes, output_sr)

    elapsed = time.time() - start
    audio_len = len(pcm_bytes) / 2 / output_sr
    logger.info(f"tts: done, rtf={elapsed/audio_len:.3f}, audio={audio_len:.2f}s, "
                f"wav={len(wav_bytes)} bytes")

    return Response(content=wav_bytes, media_type='audio/wav')


@app.post("/tts/stream")
async def tts_stream(req: TtsRequest):
    """
    流式 TTS 合成接口.
    返回 StreamingResponse: 先发 44 字节 WAV 头 (24kHz mono 16-bit, dataSize=0),
    再逐块发 raw PCM16 数据 (模型边生成边输出).
    C++ 端读取前 44 字节作为 WAV 头跳过, 后续字节直接作为 PCM16 送 waveOut 播放.
    与 /tts 的区别: /tts 等整句合成完再返回 (18s+), /tts/stream 边生成边返回 (首包 2-3s).
    """
    if cosyvoice is None:
        return Response(content=b'', media_type='audio/wav', status_code=503)

    text = req.text.strip()
    if not text:
        return Response(content=b'', media_type='audio/wav', status_code=400)

    instruct_text = apply_tone_prefix(text, req.tone)
    logger.info(f"tts/stream: text={text[:120]}, tone={req.tone}, "
                f"text_chars={len(text)}, instruct={instruct_text[:120]}")
    start = time.time()

    def generate():
        # 先发 WAV 头 (44 字节, 24kHz mono 16-bit, dataSize=0).
        # C++ 端会跳过这 44 字节, 后续字节直接作为 PCM16 送 waveOut.
        logger.info("tts/stream: sending WAV header (44 bytes)")
        yield pcm16_to_wav(b'', cosyvoice.sample_rate)

        total_bytes = 0
        first_chunk_time = None
        chunk_count = 0
        try:
            # stream=True: 模型边生成 speech token 边出 WAV chunk, 不用等整句生成完.
            # 每 chunk 约 0.5-2 秒音频, 首包约 2-3 秒到达.
            for output in cosyvoice.inference_instruct2(
                tts_text=text,
                instruct_text=instruct_text,
                prompt_wav=ref_wav_16k,
                stream=True,
            ):
                speech = output['tts_speech']  # [1, T], 24kHz float32
                speech_np = speech.numpy().squeeze(0)  # [T]
                pcm16 = (speech_np * 32767).clip(-32768, 32767).astype(np.int16)
                pcm_bytes = pcm16.tobytes()
                total_bytes += len(pcm_bytes)
                chunk_count += 1
                chunk_dur = speech_np.shape[0] / cosyvoice.sample_rate
                if first_chunk_time is None:
                    first_chunk_time = time.time()
                    logger.info(f"tts/stream: first chunk, latency={first_chunk_time - start:.3f}s, "
                                f"chunk_samples={speech_np.shape[0]}, "
                                f"chunk_dur={chunk_dur:.3f}s, "
                                f"chunk_bytes={len(pcm_bytes)}")
                else:
                    logger.info(f"tts/stream: chunk #{chunk_count}, "
                                f"dur={chunk_dur:.3f}s, bytes={len(pcm_bytes)}, "
                                f"cumulative={total_bytes / 2 / cosyvoice.sample_rate:.3f}s")
                yield pcm_bytes
        except Exception as e:
            import traceback
            logger.error(f"tts/stream: inference failed: {e}\n{traceback.format_exc()}")
            return

        elapsed = time.time() - start
        audio_len = total_bytes / 2 / cosyvoice.sample_rate
        if audio_len > 0:
            first_lat = (first_chunk_time - start) if first_chunk_time else 0
            rtf = elapsed / audio_len
            logger.info(f"tts/stream: done, rtf={rtf:.3f}, "
                        f"audio={audio_len:.3f}s, bytes={total_bytes}, "
                        f"chunks={chunk_count}, first_lat={first_lat:.3f}s, "
                        f"total_elapsed={elapsed:.3f}s")
        else:
            logger.warning(f"tts/stream: done with no audio, chunks={chunk_count}")

    return StreamingResponse(generate(), media_type="audio/wav")


@app.get("/health")
async def health():
    """健康检查接口."""
    return {"status": "ok", "model_loaded": cosyvoice is not None}


def main():
    global cosyvoice, ref_wav_16k, ref_text, sample_rate

    parser = argparse.ArgumentParser(description='Swallow local TTS server (CosyVoice2)')
    parser.add_argument('--model_dir', type=str, required=True,
                        help='CosyVoice2 model directory')
    parser.add_argument('--ref_wav', type=str, required=True,
                        help='Reference audio WAV for voice cloning')
    parser.add_argument('--ref_text', type=str, required=True,
                        help='Transcript of reference audio')
    parser.add_argument('--port', type=int, default=9880,
                        help='Server port (default: 9880)')
    parser.add_argument('--host', type=str, default='127.0.0.1',
                        help='Server host (default: 127.0.0.1)')
    args = parser.parse_args()

    # 检测 PyTorch GPU 状态, 明确告诉用户推理在哪个设备上.
    torch_ver = torch.__version__
    if torch.cuda.is_available():
        gpu_name = torch.cuda.get_device_name(0)
        cuda_ver = torch.version.cuda
        logger.info(f"PyTorch GPU 模式: torch={torch_ver}, CUDA={cuda_ver}, GPU={gpu_name}")
    else:
        logger.warning(f"PyTorch CPU 模式: torch={torch_ver}, CUDA 不可用, 推理将使用 CPU (速度慢)")

    # 检测 ONNX Runtime 执行提供者 (CosyVoice2 的 speech_tokenizer 用 ONNX 推理).
    # ONNX CUDA provider 在本机可能加载失败 (dll 不兼容), 会自动回退到 CPU.
    try:
        import onnxruntime as ort
        providers = ort.get_available_providers()
        logger.info(f"ONNX Runtime providers: {providers}")
    except Exception:
        logger.warning("ONNX Runtime not available")

    logger.info(f"loading CosyVoice2 model from {args.model_dir}...")
    # AutoModel 会根据目录下的 cosyvoice2.yaml 自动选择 CosyVoice2 类.
    cosyvoice = AutoModel(model_dir=args.model_dir, load_jit=True, fp16=True)
    sample_rate = cosyvoice.sample_rate
    logger.info(f"model loaded, sample_rate={sample_rate}, fp16=True, load_jit=True")

    # 加载参考音频 (16kHz), 用于 zero-shot 音色克隆.
    # 用 scipy 替代 torchaudio (torchaudio 在本机不可用).
    logger.info(f"loading reference audio: {args.ref_wav}")
    ref_wav_16k = scipy_load_wav(args.ref_wav, 16000)
    ref_text = args.ref_text
    logger.info(f"reference audio loaded, shape={ref_wav_16k.shape}")

    logger.info(f"starting TTS server on {args.host}:{args.port}")
    uvicorn.run(app, host=args.host, port=args.port, log_level='info',
               log_config=None)


if __name__ == '__main__':
    main()
