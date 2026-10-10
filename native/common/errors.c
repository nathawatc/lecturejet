#include "common.h"

#include <stdarg.h>
#include <stdio.h>

void lj_error(char *error, size_t error_size, const char *format, ...)
{
	if (!error || error_size == 0)
		return;
	va_list args;
	va_start(args, format);
	vsnprintf(error, error_size, format, args);
	va_end(args);
	error[error_size - 1] = '\0';
}

void lj_fferror(char *error, size_t error_size, const char *operation, int code)
{
	char detail[AV_ERROR_MAX_STRING_SIZE];
	av_strerror(code, detail, sizeof(detail));
	lj_error(error, error_size, "%s: %s", operation, detail);
}

void lj_report_failure(char *error, size_t error_size, const char *detail,
	const LJCancelToken *cancel, const char *operation, int code)
{
	if (lj_cancel_requested(cancel))
		lj_error(error, error_size, "operation cancelled");
	else if (detail && detail[0])
		lj_error(error, error_size, "%s", detail);
	else
		lj_fferror(error, error_size, operation, code);
}
