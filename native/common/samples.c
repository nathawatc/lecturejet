#include "common.h"

#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#define LJ_SAMPLES_MIN_CAPACITY 4096

float *lj_samples_extend(LJSampleBuffer *buffer, size_t frames)
{
	size_t channels = (size_t)buffer->channels;
	size_t limit = SIZE_MAX / channels / sizeof(float);
	if (frames > limit - buffer->frames)
		return NULL;
	size_t required = buffer->frames + frames;
	if (required > buffer->capacity) {
		size_t capacity = buffer->capacity ? buffer->capacity : LJ_SAMPLES_MIN_CAPACITY;
		while (capacity < required)
			capacity = capacity > limit / 2 ? limit : capacity * 2;
		float *grown = realloc(buffer->data, capacity * channels * sizeof(float));
		if (!grown)
			return NULL;
		buffer->data = grown;
		buffer->capacity = capacity;
	}
	float *tail = buffer->data + buffer->frames * channels;
	buffer->frames = required;
	return tail;
}

void lj_samples_discard(LJSampleBuffer *buffer, size_t frames)
{
	size_t channels = (size_t)buffer->channels;
	if (frames > buffer->frames)
		frames = buffer->frames;
	buffer->frames -= frames;
	if (buffer->frames > 0)
		memmove(buffer->data, buffer->data + frames * channels,
			buffer->frames * channels * sizeof(float));
}

void lj_samples_free(LJSampleBuffer *buffer)
{
	free(buffer->data);
	memset(buffer, 0, sizeof(*buffer));
}
