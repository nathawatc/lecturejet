#if defined(__has_include)
#if __has_include("_cgo_export.h")
#include "_cgo_export.h"
#endif
#endif

extern void lecturejetProgress(int permille);

#include "../../native/lecturejet.c"

void lj_go_progress(int permille)
{
	lecturejetProgress(permille);
}
