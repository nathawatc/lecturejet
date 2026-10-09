#include "internal.h"

int lj_remux_hevc_hvc1(const char *input, const char *output,
	LJCancelToken *cancel, char *error, size_t error_size)
{
	if (error && error_size > 0)
		error[0] = '\0';
	if (!input || !output) {
		lj_error(error, error_size, "remuxing: invalid argument");
		return AVERROR(EINVAL);
	}

	AVFormatContext *input_format = NULL;
	AVFormatContext *output_format = NULL;
	AVPacket *packet = NULL;
	int ret = lj_abort_if_cancelled(cancel, error, error_size);
	int has_hevc = 0;
	if (ret < 0)
		goto done;

	input_format = avformat_alloc_context();
	if (!input_format) {
		ret = AVERROR(ENOMEM);
		goto done;
	}
	lj_set_interrupt_callback(input_format, cancel);
	ret = avformat_open_input(&input_format, input, NULL, NULL);
	if (ret < 0)
		goto done;
	ret = avformat_find_stream_info(input_format, NULL);
	if (ret < 0)
		goto done;

	ret = avformat_alloc_output_context2(&output_format, NULL, "mp4", output);
	if (ret < 0 || !output_format) {
		if (ret >= 0)
			ret = AVERROR_UNKNOWN;
		goto done;
	}
	lj_set_interrupt_callback(output_format, cancel);
	ret = av_dict_copy(&output_format->metadata, input_format->metadata, 0);
	if (ret < 0)
		goto done;

	for (unsigned int i = 0; i < input_format->nb_streams; ++i) {
		const AVStream *input_stream = input_format->streams[i];
		AVStream *output_stream = avformat_new_stream(output_format, NULL);
		if (!output_stream) {
			ret = AVERROR(ENOMEM);
			goto done;
		}
		ret = avcodec_parameters_copy(output_stream->codecpar, input_stream->codecpar);
		if (ret < 0)
			goto done;
		output_stream->id = input_stream->id;
		output_stream->time_base = input_stream->time_base;
		output_stream->start_time = input_stream->start_time;
		output_stream->duration = input_stream->duration;
		output_stream->nb_frames = input_stream->nb_frames;
		output_stream->disposition = input_stream->disposition;
		output_stream->discard = input_stream->discard;
		output_stream->sample_aspect_ratio = input_stream->sample_aspect_ratio;
		output_stream->avg_frame_rate = input_stream->avg_frame_rate;
		output_stream->r_frame_rate = input_stream->r_frame_rate;
		ret = av_dict_copy(&output_stream->metadata, input_stream->metadata, 0);
		if (ret < 0)
			goto done;
		if (input_stream->disposition & AV_DISPOSITION_ATTACHED_PIC) {
			ret = av_packet_ref(&output_stream->attached_pic, &input_stream->attached_pic);
			if (ret < 0)
				goto done;
		}
		if (input_stream->codecpar->codec_type == AVMEDIA_TYPE_VIDEO &&
			input_stream->codecpar->codec_id == AV_CODEC_ID_HEVC) {
			output_stream->codecpar->codec_tag = lj_fourcc("hvc1");
			has_hevc = 1;
		}
	}
	if (!has_hevc) {
		lj_error(error, error_size, "remuxing: input contains no HEVC video stream");
		ret = AVERROR_STREAM_NOT_FOUND;
		goto done;
	}

	if (!(output_format->oformat->flags & AVFMT_NOFILE)) {
		ret = avio_open2(&output_format->pb, output, AVIO_FLAG_WRITE,
			&output_format->interrupt_callback, NULL);
		if (ret < 0)
			goto done;
	}
	ret = lj_abort_if_cancelled(cancel, error, error_size);
	if (ret < 0)
		goto done;
	ret = avformat_write_header(output_format, NULL);
	if (ret < 0)
		goto done;

	packet = av_packet_alloc();
	if (!packet) {
		ret = AVERROR(ENOMEM);
		goto done;
	}
	for (;;) {
		ret = lj_abort_if_cancelled(cancel, error, error_size);
		if (ret < 0)
			goto done;
		ret = av_read_frame(input_format, packet);
		if (ret == AVERROR_EOF) {
			ret = 0;
			break;
		}
		if (ret < 0)
			goto done;
		ret = lj_abort_if_cancelled(cancel, error, error_size);
		if (ret < 0) {
			av_packet_unref(packet);
			goto done;
		}
		if (packet->stream_index < 0 ||
			(unsigned int)packet->stream_index >= input_format->nb_streams) {
			lj_error(error, error_size, "remuxing: packet has invalid stream index %d",
				packet->stream_index);
			ret = AVERROR_INVALIDDATA;
			av_packet_unref(packet);
			goto done;
		}
		AVStream *input_stream = input_format->streams[packet->stream_index];
		AVStream *output_stream = output_format->streams[packet->stream_index];
		av_packet_rescale_ts(packet, input_stream->time_base, output_stream->time_base);
		packet->stream_index = output_stream->index;
		packet->pos = -1;
		ret = av_interleaved_write_frame(output_format, packet);
		av_packet_unref(packet);
		if (ret < 0)
			goto done;
	}
	ret = lj_abort_if_cancelled(cancel, error, error_size);
	if (ret >= 0)
		ret = av_write_trailer(output_format);
	if (ret >= 0)
		ret = lj_abort_if_cancelled(cancel, error, error_size);

done:
	av_packet_free(&packet);
	avformat_close_input(&input_format);
	if (output_format) {
		if (!(output_format->oformat->flags & AVFMT_NOFILE)) {
			int close_ret = avio_closep(&output_format->pb);
			if (ret >= 0 && close_ret < 0) {
				ret = close_ret;
				if (lj_cancel_requested(cancel))
					lj_error(error, error_size, "operation cancelled");
				else
					lj_fferror(error, error_size, "closing remux output", ret);
			}
		}
		avformat_free_context(output_format);
	}
	if (ret < 0 && (!error || error_size == 0 || !error[0])) {
		if (lj_cancel_requested(cancel))
			lj_error(error, error_size, "operation cancelled");
		else if (ret == AVERROR_EOF)
			lj_error(error, error_size, "remuxing: unexpected end of input");
		else
			lj_fferror(error, error_size, "remuxing", ret);
	}
	return ret;
}
