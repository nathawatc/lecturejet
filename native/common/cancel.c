#include "common.h"

#include <stdlib.h>

LJCancelToken *lj_cancel_token_create(void)
{
	return calloc(1, sizeof(LJCancelToken));
}

void lj_cancel_token_cancel(LJCancelToken *token)
{
	if (token)
		__atomic_store_n(&token->cancelled, 1, __ATOMIC_RELEASE);
}

void lj_cancel_token_destroy(LJCancelToken **token)
{
	if (!token)
		return;
	free(*token);
	*token = NULL;
}

int lj_cancel_requested(const LJCancelToken *token)
{
	return token && __atomic_load_n(&token->cancelled, __ATOMIC_ACQUIRE);
}

static int lj_ffmpeg_interrupt(void *opaque)
{
	return lj_cancel_requested((const LJCancelToken *)opaque);
}

int lj_abort_if_cancelled(const LJCancelToken *token, char *error, size_t error_size)
{
	if (!lj_cancel_requested(token))
		return 0;
	lj_error(error, error_size, "operation cancelled");
	return AVERROR_EXIT;
}

void lj_set_interrupt_callback(AVFormatContext *format, LJCancelToken *token)
{
	if (!format || !token)
		return;
	format->interrupt_callback.callback = lj_ffmpeg_interrupt;
	format->interrupt_callback.opaque = token;
}
